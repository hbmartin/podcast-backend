package handlers

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/hbmartin/podcast-backend/errs"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBindProtoRejectsOversizedBodyExplicitly(t *testing.T) {
	r := httptest.NewRequest("POST", "/", io.NopCloser(bytes.NewReader(make([]byte, maxProtoBody+1))))
	err := bindProto(r, &pb.InboxRequest{})

	require.Error(t, err)
	var structured *errs.Error
	require.True(t, errors.As(err, &structured))
	assert.Equal(t, errs.Code("body_too_large"), structured.Code)
}
