package avatars

import (
	"context"
	"encoding/base64"
	"fmt"

	vision "cloud.google.com/go/vision/v2/apiv1"
	visionpb "cloud.google.com/go/vision/v2/apiv1/visionpb"
	"google.golang.org/api/option"
)

type Scanner interface {
	Allows(context.Context, []byte) (bool, error)
}

type VisionScanner struct{ client *vision.ImageAnnotatorClient }

func NewVisionScanner(ctx context.Context, credentialsBase64 string) (*VisionScanner, error) {
	credentials, err := base64.StdEncoding.DecodeString(credentialsBase64)
	if err != nil {
		return nil, fmt.Errorf("decode Google Vision credentials: %w", err)
	}
	client, err := vision.NewImageAnnotatorClient(ctx, option.WithCredentialsJSON(credentials))
	if err != nil {
		return nil, fmt.Errorf("create Google Vision client: %w", err)
	}
	return &VisionScanner{client: client}, nil
}

func (s *VisionScanner) Allows(ctx context.Context, data []byte) (bool, error) {
	response, err := s.client.BatchAnnotateImages(ctx, &visionpb.BatchAnnotateImagesRequest{
		Requests: []*visionpb.AnnotateImageRequest{{
			Image:    &visionpb.Image{Content: data},
			Features: []*visionpb.Feature{{Type: visionpb.Feature_SAFE_SEARCH_DETECTION}},
		}},
	})
	if err != nil {
		return false, err
	}
	if len(response.Responses) == 0 {
		return false, fmt.Errorf("vision: empty SafeSearch response")
	}
	result := response.Responses[0]
	if result.Error != nil {
		return false, fmt.Errorf("vision: SafeSearch failed: %s", result.Error.Message)
	}
	annotation := result.SafeSearchAnnotation
	if annotation == nil {
		return false, fmt.Errorf("vision: missing SafeSearch annotation")
	}
	blocked := func(value visionpb.Likelihood) bool {
		return value == visionpb.Likelihood_LIKELY || value == visionpb.Likelihood_VERY_LIKELY
	}
	return !blocked(annotation.Adult) && !blocked(annotation.Racy), nil
}

func (s *VisionScanner) Close() error { return s.client.Close() }
