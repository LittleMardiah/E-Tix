package analytics

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOwners is a stub EventOwnerReader for ownership-check tests.
type fakeOwners struct {
	orgID uuid.UUID
}

func (f fakeOwners) GetOrganizerID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.orgID, nil
}

func TestEscapeCSV_FormulaInjection(t *testing.T) {
	cases := []struct{ in, want string }{
		{"=1+1", "'=1+1"},
		{"+123", "'+123"},
		{"-cmd", "'-cmd"},
		{"@user", "'@user"},
		{"normal", "normal"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, escapeCSV(c.in))
	}
}

func TestEscapeCSV_Empty(t *testing.T) {
	assert.Equal(t, "", escapeCSV(""))
}

func TestGetPostEventReport_PDFStub(t *testing.T) {
	callerID := uuid.New()
	eventID := uuid.New()
	owners := &fakeOwners{orgID: callerID}
	svc := NewAnalyticsService(nil, owners)
	_, err := svc.GetPostEventReport(context.Background(), callerID, eventID, "organizer", "pdf")
	require.ErrorIs(t, err, ErrPDFNotImplemented)
}

func TestGetDashboard_Forbidden(t *testing.T) {
	caller := uuid.New()
	other := uuid.New()
	svc := NewAnalyticsService(nil, fakeOwners{orgID: other})
	_, err := svc.GetDashboard(context.Background(), caller, uuid.New(), "organizer")
	require.ErrorIs(t, err, ErrEventForbidden)
}
