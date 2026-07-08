package syncsvc

import (
	"github.com/hbmartin/podcast-backend/errs"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Settings are stored as protojson of Api_ChangeableSettings: every populated
// field is a *Setting message ({value, changed, modifiedAt}). Merging and the
// NamedSettingsResponse projection work generically over the message fields —
// ChangeableSettings and NamedSettingsResponse share field names and numbers.

func decodeStoredSettings(raw []byte) (*pb.ChangeableSettings, error) {
	const op errs.Op = "syncsvc/decodeStoredSettings"

	stored := &pb.ChangeableSettings{}
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return stored, nil
	}
	if err := protojson.Unmarshal(raw, stored); err != nil {
		return nil, errs.E(op, errs.Internal, err)
	}
	return stored, nil
}

func encodeStoredSettings(settings *pb.ChangeableSettings) ([]byte, error) {
	const op errs.Op = "syncsvc/encodeStoredSettings"

	raw, err := protojson.Marshal(settings)
	if err != nil {
		return nil, errs.E(op, errs.Internal, err)
	}
	return raw, nil
}

// settingModifiedAt reads the modifiedAt timestamp (millis) of a *Setting
// message (BoolSetting/Int32Setting/DoubleSetting/StringSetting) generically.
func settingModifiedAt(setting protoreflect.Message) int64 {
	fd := setting.Descriptor().Fields().ByName("modified_at")
	if fd == nil || !setting.Has(fd) {
		return 0
	}
	ts, ok := setting.Get(fd).Message().Interface().(*timestamppb.Timestamp)
	if !ok {
		return 0
	}
	return ts.AsTime().UnixMilli()
}

// MergeChangedSettings applies incoming per-key setting changes onto stored,
// keeping whichever side has the newer modifiedAt for each key. Returns true
// when anything changed.
func MergeChangedSettings(stored *pb.ChangeableSettings, incoming *pb.ChangeableSettings) bool {
	if incoming == nil {
		return false
	}

	changed := false
	storedRef := stored.ProtoReflect()
	incoming.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}

		if storedRef.Has(fd) {
			existing := storedRef.Get(fd).Message()
			if settingModifiedAt(v.Message()) <= settingModifiedAt(existing) {
				return true
			}
		}
		storedRef.Set(fd, protoreflect.ValueOfMessage(v.Message()))
		changed = true
		return true
	})
	return changed
}

// ApplyLegacySettings folds the legacy NamedSettings shape (bare wrapper
// values, no modifiedAt) into stored with the given modification time.
func ApplyLegacySettings(stored *pb.ChangeableSettings, legacy *pb.NamedSettings, nowMs int64) bool {
	if legacy == nil {
		return false
	}

	changed := false
	storedRef := stored.ProtoReflect()
	storedFields := storedRef.Descriptor().Fields()
	modified := timestamppb.New(timestampFromMillis(nowMs))

	legacy.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		target := storedFields.ByName(fd.Name())
		if target == nil || target.Kind() != protoreflect.MessageKind {
			return true
		}

		// Wrap the bare value in the matching *Setting message.
		var setting proto.Message
		switch v.Message().Interface().(type) {
		case *wrapperspb.Int32Value:
			setting = &pb.Int32Setting{
				Value:      v.Message().Interface().(*wrapperspb.Int32Value),
				Changed:    wrapperspb.Bool(true),
				ModifiedAt: modified,
			}
		case *wrapperspb.BoolValue:
			setting = &pb.BoolSetting{
				Value:      v.Message().Interface().(*wrapperspb.BoolValue),
				Changed:    wrapperspb.Bool(true),
				ModifiedAt: modified,
			}
		case *wrapperspb.DoubleValue:
			setting = &pb.DoubleSetting{
				Value:      v.Message().Interface().(*wrapperspb.DoubleValue),
				Changed:    wrapperspb.Bool(true),
				ModifiedAt: modified,
			}
		case *wrapperspb.StringValue:
			setting = &pb.StringSetting{
				Value:      v.Message().Interface().(*wrapperspb.StringValue),
				Changed:    wrapperspb.Bool(true),
				ModifiedAt: modified,
			}
		default:
			return true
		}

		if setting.ProtoReflect().Descriptor().FullName() != target.Message().FullName() {
			return true
		}

		storedRef.Set(target, protoreflect.ValueOfMessage(setting.ProtoReflect()))
		changed = true
		return true
	})
	return changed
}

// BuildNamedSettingsResponse projects stored settings onto the response
// message by matching field names; only keys the server has stored are set,
// so clients' newer local values survive their modifiedAt comparison.
func BuildNamedSettingsResponse(stored *pb.ChangeableSettings) *pb.NamedSettingsResponse {
	resp := &pb.NamedSettingsResponse{}
	respRef := resp.ProtoReflect()
	respFields := respRef.Descriptor().Fields()

	stored.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		target := respFields.ByName(fd.Name())
		if target == nil || target.Kind() != protoreflect.MessageKind ||
			target.Message().FullName() != fd.Message().FullName() {
			return true
		}
		respRef.Set(target, v)
		return true
	})
	return resp
}

// MergePodcastSettings merges incoming per-podcast settings onto stored ones
// (same generic per-key modifiedAt semantics as app settings).
func MergePodcastSettings(storedRaw []byte, incoming *pb.PodcastSettings) ([]byte, error) {
	const op errs.Op = "syncsvc/MergePodcastSettings"

	stored := &pb.PodcastSettings{}
	if len(storedRaw) > 0 && string(storedRaw) != "{}" && string(storedRaw) != "null" {
		if err := protojson.Unmarshal(storedRaw, stored); err != nil {
			return nil, errs.E(op, errs.Internal, err)
		}
	}

	if incoming != nil {
		storedRef := stored.ProtoReflect()
		incoming.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			if storedRef.Has(fd) {
				existing := storedRef.Get(fd).Message()
				if settingModifiedAt(v.Message()) <= settingModifiedAt(existing) {
					return true
				}
			}
			storedRef.Set(fd, protoreflect.ValueOfMessage(v.Message()))
			return true
		})
	}

	raw, err := protojson.Marshal(stored)
	if err != nil {
		return nil, errs.E(op, errs.Internal, err)
	}
	return raw, nil
}

func decodePodcastSettings(raw []byte) *pb.PodcastSettings {
	settings := &pb.PodcastSettings{}
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return settings
	}
	if err := protojson.Unmarshal(raw, settings); err != nil {
		return &pb.PodcastSettings{}
	}
	return settings
}
