package api

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Golden wire-format tests. Expected bytes are hand-computed from the field
// numbers in the client's generated Swift (api.pb.swift); a mismatch means a
// transcription error that would silently break the iOS client.

func mustHex(t *testing.T, m proto.Message) string {
	t.Helper()
	b, err := proto.Marshal(m)
	assert.NoError(t, err)
	return hex.EncodeToString(b)
}

func TestUserLoginRequestWire(t *testing.T) {
	msg := &UserLoginRequest{Email: "a@b.co", Password: "pw"}
	// field 1 string "a@b.co": 0a 06 <6 bytes>; field 2 string "pw": 12 02 <2 bytes>
	assert.Equal(t, "0a066140622e636f12027077", mustHex(t, msg))
}

func TestUserChangePasswordRequestWire(t *testing.T) {
	// scope is field 4 (gap at 3) in the client contract
	msg := &UserChangePasswordRequest{OldPassword: "old", NewPassword: "new", Scope: "mobile"}
	assert.Equal(t, "0a036f6c6412036e657722066d6f62696c65", mustHex(t, msg))
}

func TestSyncUserEpisodeStarredWire(t *testing.T) {
	// starred is field 11, a google.protobuf.BoolValue wrapper: 5a 02 08 01
	msg := &SyncUserEpisode{Uuid: "u", Starred: wrapperspb.Bool(true)}
	assert.Equal(t, "0a01755a020801", mustHex(t, msg))
}

func TestHistoryChangeWire(t *testing.T) {
	// action=2 (delete) field 1 varint: 08 02; episode field 3 string: 1a 01 65
	msg := &HistoryChange{Action: 2, Episode: "e"}
	assert.Equal(t, "08021a0165", mustHex(t, msg))
}

func TestRecordOneofWire(t *testing.T) {
	// Record.episode is oneof field 2: nested message tag 12
	msg := &Record{Record: &Record_Episode{Episode: &SyncUserEpisode{Uuid: "u"}}}
	assert.Equal(t, "12030a0175", mustHex(t, msg))
}

func TestPodcastRatingWire(t *testing.T) {
	// field 2 is intentionally unused in the client contract: modified_at is
	// field 3 (Timestamp) and podcast_rating is field 4 (uint32)
	msg := &PodcastRating{
		PodcastUuid:   "u",
		ModifiedAt:    &timestamppb.Timestamp{Seconds: 1},
		PodcastRating: 5,
	}
	assert.Equal(t, "0a01751a0208012005", mustHex(t, msg))
}

func TestStatsResponseWire(t *testing.T) {
	msg := &StatsResponse{
		TimeSilenceRemoval: 1,
		TimeSkipping:       2,
		TimeIntroSkipping:  3,
		TimeVariableSpeed:  4,
		TimeListened:       5,
		TimesStartedAt:     &timestamppb.Timestamp{Seconds: 6},
	}
	assert.Equal(t, "0801100218032004280532020806", mustHex(t, msg))
}

func TestRoundTripCoreMessages(t *testing.T) {
	msgs := []proto.Message{
		&UserLoginRequest{Email: "e", Password: "p", Scope: "mobile", Dt: "1", Device: "d", V: "1.7", M: "iPhone", Av: "7.0", F: "0", L: "en", C: "US"},
		&UserLoginResponse{Token: "t", Uuid: "u", Email: "e"},
		&RegisterResponse{Success: wrapperspb.Bool(true), Token: "t", Uuid: "u", Errors: []string{"x"}},
		&TokenLoginResponse{Email: "e", Uuid: "u", IsNew: true, AccessToken: "a", TokenType: "Bearer", ExpiresIn: 3600, RefreshToken: "r"},
		&SyncUpdateRequest{DeviceUtcTimeMs: 1, LastModified: 2, Country: "US", DeviceId: "d",
			DeviceType: wrapperspb.Int32(1),
			Records: []*Record{
				{Record: &Record_Podcast{Podcast: &SyncUserPodcast{Uuid: "p", Subscribed: wrapperspb.Bool(true)}}},
				{Record: &Record_Folder{Folder: &SyncUserFolder{FolderUuid: "f", Name: "n", Color: 3}}},
				{Record: &Record_Bookmark{Bookmark: &SyncUserBookmark{BookmarkUuid: "b", Time: wrapperspb.Int32(10)}}},
			}},
		&UpNextChanges{ServerModified: 5, Changes: []*UpNextChanges_Change{{Uuid: "u", Action: 2, Modified: 9}}, Order: []string{"a", "b"}},
		&UpNextResponse{ServerModified: 5,
			Episodes:    []*UpNextResponse_EpisodeResponse{{Uuid: "u", Title: "t"}},
			EpisodeSync: []*UpNextResponse_EpisodeSyncResponse{{Uuid: "u", PlayedUpTo: wrapperspb.Int32(2)}}},
		&HistoryResponse{ServerModified: 1, LastCleared: 2, Changes: []*HistoryChange{{Action: 1, Episode: "e"}}},
		&NamedSettingsRequest{V: "1", M: "iPhone", ChangedSettings: &ChangeableSettings{
			SkipForward: &Int32Setting{Value: wrapperspb.Int32(30), Changed: wrapperspb.Bool(true)},
		}},
		&SyncEpisodesResponse{Episodes: []*EpisodeSyncResponse{{Uuid: "u", PlayingStatus: 2, Bookmarks: []*BookmarkResponse{{BookmarkUuid: "b", Time: 5}}}}},
		&StarredEpisodesResponse{Episodes: []*StarredEpisode{{Uuid: "u", StarredModified: 4}}},
		&UserPodcastListResponse{
			Podcasts: []*UserPodcastResponse{{Uuid: "p", Title: "t", FolderUuid: wrapperspb.String("f")}},
			Folders:  []*PodcastFolder{{FolderUuid: "f", Name: "n"}}},
		&UpdateEpisodeRequest{Uuid: "u", Podcast: "p", Position: wrapperspb.Int32(10), Status: 2, Duration: 100},
		&PodcastRatingAddRequest{PodcastUuid: "p", PodcastRating: 4},
		&PodcastRatingsResponse{PodcastRatings: []*PodcastRating{{PodcastUuid: "p", PodcastRating: 4, ModifiedAt: &timestamppb.Timestamp{Seconds: 9}}}},
		&StatsResponse{TimeListened: 100, TimesStartedAt: &timestamppb.Timestamp{Seconds: 6}},
	}

	for _, m := range msgs {
		b, err := proto.Marshal(m)
		assert.NoError(t, err)

		clone := m.ProtoReflect().New().Interface()
		assert.NoError(t, proto.Unmarshal(b, clone))
		assert.True(t, proto.Equal(m, clone), "round trip mismatch for %T", m)
	}
}

// Fork-owned transcript messages (docs/TranscriptContributions.md §3). Field
// numbers must match the iOS client's generated api.pb.swift.
func TestTranscriptContributionRequestWire(t *testing.T) {
	msg := &TranscriptContributionRequest{
		EpisodeUuid:            "e",
		PodcastUuid:            "p",
		Vtt:                    []byte{0x01, 0x02},
		Fingerprint:            []byte{0x03},
		Engine:                 "w",
		ModelId:                "m",
		Language:               "l",
		Diarized:               true,
		AppVersion:             "a",
		EpisodeDurationSeconds: 1,
		CreatedAt:              &timestamppb.Timestamp{Seconds: 1, Nanos: 2},
	}
	// f1-f9 are length-delimited except f8 (bool); f10 is fixed64 double;
	// f11 is a Timestamp containing seconds=1 and nanos=2.
	assert.Equal(t, "0a01651201701a0201022201032a017732016d3a016c40014a016151000000000000f03f5a0408011002", mustHex(t, msg))
}

func TestTranscriptSightingRequestWire(t *testing.T) {
	msg := &TranscriptSightingRequest{EpisodeUuid: "e", PodcastUuid: "p", TranscriptUrl: "u", Format: "f", Language: "l"}
	// f1 0a0165, f2 120170, f3 1a0175, f4 220166, f5 2a016c
	assert.Equal(t, "0a01651201701a01752201662a016c", mustHex(t, msg))
}

func TestTranscriptContributionRequestRoundTrip(t *testing.T) {
	in := &TranscriptContributionRequest{
		EpisodeUuid:            "ep",
		PodcastUuid:            "pod",
		Vtt:                    []byte{1, 2, 3},
		Fingerprint:            []byte{4, 5},
		Engine:                 "whisperkit",
		ModelId:                "whisper-large-v3-turbo",
		Language:               "en",
		Diarized:               true,
		AppVersion:             "1.0",
		EpisodeDurationSeconds: 1234.5,
		CreatedAt:              timestamppb.New(timestamppb.Now().AsTime()),
	}
	b, err := proto.Marshal(in)
	assert.NoError(t, err)
	out := &TranscriptContributionRequest{}
	assert.NoError(t, proto.Unmarshal(b, out))
	assert.Equal(t, in.EpisodeUuid, out.EpisodeUuid)
	assert.Equal(t, in.Vtt, out.Vtt)
	assert.Equal(t, in.Diarized, out.Diarized)
	assert.Equal(t, in.EpisodeDurationSeconds, out.EpisodeDurationSeconds)
}
