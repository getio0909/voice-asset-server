package httpapi

import (
	"context"

	"github.com/getio0909/voice-asset-server/internal/audit"
)

type fakeAuditService struct {
	input audit.RecordInput
	err   error
	calls int
}

func (service *fakeAuditService) Record(_ context.Context, input audit.RecordInput) error {
	service.calls++
	service.input = input
	return service.err
}

var _ AuditService = (*fakeAuditService)(nil)
