package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/middlewares"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/jackc/pgx/v5"
	xdraw "golang.org/x/image/draw"
)

const (
	MaxAvatarUpload    = 10 << 20
	maxAvatarPixels    = 40_000_000
	maxAvatarDimension = 12_000
)

func (h Handlers) PostSocialAvatar(w http.ResponseWriter, r *http.Request) {
	if h.ObjectStore == nil || h.AvatarScanner == nil {
		http.NotFound(w, r)
		return
	}
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}
	if _, err := h.Queries.GetSocialProfileByUserID(r.Context(), user.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		writeError(w, r, err)
		return
	}
	if !h.allowAvatarUpload(w, r, user.ID) {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxAvatarUpload+1))
	if err != nil || len(raw) > MaxAvatarUpload {
		writeProto(w, http.StatusOK, &pb.AvatarUploadResponse{Status: pb.AvatarUploadStatus_AVATAR_UPLOAD_STATUS_REJECTED_FORMAT})
		return
	}
	normalized, err := normalizeAvatar(raw)
	if err != nil {
		writeProto(w, http.StatusOK, &pb.AvatarUploadResponse{Status: pb.AvatarUploadStatus_AVATAR_UPLOAD_STATUS_REJECTED_FORMAT})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	allowed, err := h.AvatarScanner.Allows(ctx, normalized)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !allowed {
		writeProto(w, http.StatusOK, &pb.AvatarUploadResponse{Status: pb.AvatarUploadStatus_AVATAR_UPLOAD_STATUS_REJECTED_SCAN})
		return
	}

	version := uuid.NewString()
	capability := make([]byte, 32)
	if _, err := rand.Read(capability); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	capabilityToken := base64.RawURLEncoding.EncodeToString(capability)
	capabilitySum := sha256.Sum256([]byte(capabilityToken))
	contentSum := sha256.Sum256(normalized)
	objectKey := "avatars/" + user.Uuid + "/" + version + ".jpg"
	if err := h.ObjectStore.Put(r.Context(), objectKey, normalized, "image/jpeg"); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	avatarURL := h.baseURL(r) + "/social/avatar/" + version + "/" + capabilityToken + ".jpg"
	var previous string
	err = h.Queries.InTx(r.Context(), func(q db.Querier) error {
		if existing, err := q.GetSocialAvatarByUserID(r.Context(), user.ID); err == nil {
			previous = existing.ObjectKey
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err := q.UpsertSocialAvatar(r.Context(), db.UpsertSocialAvatarParams{
			UserID: user.ID, CapabilityHash: hex.EncodeToString(capabilitySum[:]), Version: version,
			ObjectKey: objectKey, ContentHash: hex.EncodeToString(contentSum[:]),
		})
		if err != nil {
			return err
		}
		if _, err := q.SetSocialProfileAvatarURL(r.Context(), db.SetSocialProfileAvatarURLParams{UserID: user.ID, AvatarUrl: avatarURL}); err != nil {
			return err
		}
		if previous != "" && previous != objectKey {
			return q.EnqueueObjectDelete(r.Context(), db.EnqueueObjectDeleteParams{ObjectKey: previous, Reason: "avatar_replaced"})
		}
		return nil
	})
	if err != nil {
		_ = h.ObjectStore.Delete(r.Context(), objectKey)
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.AvatarUploadResponse{Status: pb.AvatarUploadStatus_AVATAR_UPLOAD_STATUS_ACCEPTED, AvatarUrl: avatarURL})
}

func (h Handlers) allowAvatarUpload(w http.ResponseWriter, r *http.Request, userID int64) bool {
	checks := []struct {
		key   string
		limit int64
	}{
		{"avatar:user:" + strconv.FormatInt(userID, 10), 5},
		{"avatar:ip:" + middlewares.ClientIP(r, h.TrustProxyHeaders), 20},
	}
	for _, check := range checks {
		allowed, retry, err := redisLimit(r.Context(), h.Redis, check.key, check.limit, time.Hour)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return false
		}
		if !allowed {
			writeRateLimited(w, retry)
			return false
		}
	}
	return true
}

func (h Handlers) DeleteSocialAvatar(w http.ResponseWriter, r *http.Request) {
	if h.ObjectStore == nil || h.AvatarScanner == nil {
		http.NotFound(w, r)
		return
	}
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}
	err := h.Queries.InTx(r.Context(), func(q db.Querier) error {
		objectKey, err := q.DeleteSocialAvatar(r.Context(), user.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := q.SetSocialProfileAvatarURL(r.Context(), db.SetSocialProfileAvatarURLParams{UserID: user.ID, AvatarUrl: ""}); err != nil {
			return err
		}
		if objectKey != "" {
			return q.EnqueueObjectDelete(r.Context(), db.EnqueueObjectDeleteParams{ObjectKey: objectKey, Reason: "avatar_deleted"})
		}
		return nil
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h Handlers) GetSocialAvatar(w http.ResponseWriter, r *http.Request) {
	if h.ObjectStore == nil {
		http.NotFound(w, r)
		return
	}
	file := r.PathValue("file")
	if !strings.HasSuffix(file, ".jpg") {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimSuffix(file, ".jpg")
	if len(token) < 32 || len(token) > 64 {
		http.NotFound(w, r)
		return
	}
	sum := sha256.Sum256([]byte(token))
	avatar, err := h.Queries.GetSocialAvatarByCapabilityHash(r.Context(), hex.EncodeToString(sum[:]))
	if err != nil || avatar.Version != r.PathValue("version") {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, err)
			return
		}
		http.NotFound(w, r)
		return
	}
	object, err := h.ObjectStore.Get(r.Context(), avatar.ObjectKey, 2<<20)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("ETag", `"`+avatar.ContentHash+`"`)
	if r.Header.Get("If-None-Match") == `"`+avatar.ContentHash+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(object.Body)
}

func normalizeAvatar(raw []byte) ([]byte, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errors.New("unsupported avatar format")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > maxAvatarDimension || config.Height > maxAvatarDimension || int64(config.Width)*int64(config.Height) > maxAvatarPixels {
		return nil, errors.New("avatar dimensions exceed limits")
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	decoded = orientJPEG(decoded, jpegOrientation(raw))
	bounds := decoded.Bounds()
	side := bounds.Dx()
	if bounds.Dy() < side {
		side = bounds.Dy()
	}
	x0 := bounds.Min.X + (bounds.Dx()-side)/2
	y0 := bounds.Min.Y + (bounds.Dy()-side)/2
	crop := image.NewRGBA(image.Rect(0, 0, side, side))
	xdraw.Draw(crop, crop.Bounds(), decoded, image.Pt(x0, y0), xdraw.Src)
	resized := image.NewRGBA(image.Rect(0, 0, 1024, 1024))
	xdraw.CatmullRom.Scale(resized, resized.Bounds(), crop, crop.Bounds(), xdraw.Over, nil)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, resized, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func orientJPEG(source image.Image, orientation int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if orientation < 2 || orientation > 8 {
		return source
	}
	outWidth, outHeight := width, height
	if orientation >= 5 {
		outWidth, outHeight = height, width
	}
	rotated := image.NewRGBA(image.Rect(0, 0, outWidth, outHeight))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var nx, ny int
			switch orientation {
			case 2:
				nx, ny = width-1-x, y
			case 3:
				nx, ny = width-1-x, height-1-y
			case 4:
				nx, ny = x, height-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = height-1-y, x
			case 7:
				nx, ny = height-1-y, width-1-x
			case 8:
				nx, ny = y, width-1-x
			}
			rotated.Set(nx, ny, source.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return rotated
}

func jpegOrientation(data []byte) int {
	// JPEG APP1/Exif stores a tiny TIFF directory. Reject malformed metadata
	// by treating it as orientation 1; image content remains independently decoded.
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xff {
			break
		}
		marker := data[offset+1]
		offset += 2
		if marker == 0xd9 || marker == 0xda {
			break
		}
		if offset+2 > len(data) {
			break
		}
		length := int(data[offset])<<8 | int(data[offset+1])
		if length < 2 || offset+length > len(data) {
			break
		}
		segment := data[offset+2 : offset+length]
		if marker == 0xe1 && len(segment) >= 14 && string(segment[:6]) == "Exif\x00\x00" {
			if orientation := tiffOrientation(segment[6:]); orientation != 0 {
				return orientation
			}
		}
		offset += length
	}
	return 1
}

func tiffOrientation(data []byte) int {
	if len(data) < 8 {
		return 0
	}
	little := string(data[:2]) == "II"
	if !little && string(data[:2]) != "MM" {
		return 0
	}
	u16 := func(b []byte) uint16 {
		if little {
			return uint16(b[0]) | uint16(b[1])<<8
		}
		return uint16(b[0])<<8 | uint16(b[1])
	}
	u32 := func(b []byte) uint32 {
		if little {
			return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
		}
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	offset := int(u32(data[4:8]))
	if offset < 0 || offset+2 > len(data) {
		return 0
	}
	count := int(u16(data[offset : offset+2]))
	offset += 2
	for i := 0; i < count && offset+12 <= len(data); i, offset = i+1, offset+12 {
		if u16(data[offset:offset+2]) == 0x0112 {
			value := int(u16(data[offset+8 : offset+10]))
			if value >= 1 && value <= 8 {
				return value
			}
		}
	}
	return 0
}
