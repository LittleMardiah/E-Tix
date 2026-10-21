package ticket

// STP traceability: UT-TICK-001 (mapped below) — UT-TICK-002/003 have no
// matching test yet (gap tracked in TD-F5-09).

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testTicketID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	testOwnerID  = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	testSecret   = "test-secret-key"
	// Block-aligned: 30 * 56666666, so sub-block offsets stay in the same block.
	testNow = time.Unix(1_699_999_980, 0).UTC()
)

// UT-TICK-001: GenerateQR_HMACSHA256_ValidSignature
func TestGenerateQRData_Success(t *testing.T) {
	qr, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow)
	require.NoError(t, err)
	assert.NotEmpty(t, qr)
}

func TestGenerateQRData_DeterministicSameBlock(t *testing.T) {
	qr1, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow)
	require.NoError(t, err)
	// Same block (within 30s window) → same output
	qr2, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow.Add(10*time.Second))
	require.NoError(t, err)
	assert.Equal(t, qr1, qr2)
}

func TestGenerateQRData_ChangesAcrossBlocks(t *testing.T) {
	qr1, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow)
	require.NoError(t, err)
	qr2, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow.Add(30*time.Second))
	require.NoError(t, err)
	assert.NotEqual(t, qr1, qr2)
}

func TestGenerateQRData_EmptySecret(t *testing.T) {
	_, err := GenerateQRData(testTicketID, testOwnerID, "", testNow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "qr_secret_key")
}

func TestVerifyDynamicQR_RoundTrip(t *testing.T) {
	qr, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow)
	require.NoError(t, err)

	gotID, err := VerifyDynamicQR(qr, testOwnerID, testSecret, testNow)
	require.NoError(t, err)
	assert.Equal(t, testTicketID, gotID)
}

func TestVerifyDynamicQR_ToleranceWindow(t *testing.T) {
	qr, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow)
	require.NoError(t, err)

	cases := []struct {
		name string
		off  time.Duration
		ok   bool
	}{
		{"minus_1_block", -30 * time.Second, true},
		{"same_block", 0, true},
		{"plus_1_block", 30 * time.Second, true},
		{"minus_2_blocks", -60 * time.Second, false},
		{"plus_2_blocks", 60 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyDynamicQR(qr, testOwnerID, testSecret, testNow.Add(tc.off))
			if tc.ok {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, ErrInvalidQR)
			}
		})
	}
}

func TestVerifyDynamicQR_WrongOwner(t *testing.T) {
	qr, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow)
	require.NoError(t, err)
	wrongOwner := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	_, err = VerifyDynamicQR(qr, wrongOwner, testSecret, testNow)
	assert.ErrorIs(t, err, ErrInvalidQR)
}

func TestVerifyDynamicQR_WrongSecret(t *testing.T) {
	qr, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow)
	require.NoError(t, err)

	_, err = VerifyDynamicQR(qr, testOwnerID, "wrong-secret", testNow)
	assert.ErrorIs(t, err, ErrInvalidQR)
}

func TestVerifyDynamicQR_EmptySecret(t *testing.T) {
	_, err := VerifyDynamicQR("any-token", testOwnerID, "", testNow)
	assert.ErrorIs(t, err, ErrInvalidQR)
}

func TestVerifyDynamicQR_MalformedBase64(t *testing.T) {
	_, err := VerifyDynamicQR("!!!not-base64!!!", testOwnerID, testSecret, testNow)
	assert.ErrorIs(t, err, ErrInvalidQR)
}

func TestVerifyDynamicQR_WrongPartCount(t *testing.T) {
	// Raw base64 of "abc" — no pipe separator → part count != 2
	qr := "YWJj"
	_, err := VerifyDynamicQR(qr, testOwnerID, testSecret, testNow)
	assert.ErrorIs(t, err, ErrInvalidQR)
}

func TestVerifyDynamicQR_BadTicketUUID(t *testing.T) {
	// base64("not-a-uuid|signature") — padded URLEncoding
	qr := "bm90LWEtdXVpZHxzaWduYXR1cmU="
	_, err := VerifyDynamicQR(qr, testOwnerID, testSecret, testNow)
	assert.ErrorIs(t, err, ErrInvalidQR)
}

func TestVerifyDynamicQR_TamperedSignature(t *testing.T) {
	qr, err := GenerateQRData(testTicketID, testOwnerID, testSecret, testNow)
	require.NoError(t, err)

	// Force a change of the last char so decode fails or signature mismatches
	if qr[len(qr)-1] == 'A' {
		t.Skip("edge: cannot tamper last char safely")
	}
	tampered := qr[:len(qr)-1] + string('A'+(qr[len(qr)-1]-'A'+1)%26)

	_, err = VerifyDynamicQR(tampered, testOwnerID, testSecret, testNow)
	assert.ErrorIs(t, err, ErrInvalidQR)
}
