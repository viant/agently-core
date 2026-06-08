package goal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeControllerSpec(t *testing.T) {
	testCases := []struct {
		name    string
		raw     string
		wantNil bool
		wantErr bool
	}{
		{name: "empty yields nil spec", raw: "", wantNil: true},
		{name: "whitespace yields nil spec", raw: "   ", wantNil: true},
		{
			name: "valid idle spec",
			raw:  `{"continueMode":"idle_only","onTurnFinished":"evaluate","onAsyncCompleted":"evaluate"}`,
		},
		{
			name:    "unknown continue mode",
			raw:     `{"continueMode":"forever","onTurnFinished":"evaluate","onAsyncCompleted":"evaluate"}`,
			wantErr: true,
		},
		{
			name:    "missing turn policy",
			raw:     `{"continueMode":"idle_only","onAsyncCompleted":"evaluate"}`,
			wantErr: true,
		},
		{
			name:    "malformed json",
			raw:     `{"continueMode":`,
			wantErr: true,
		},
		{
			name:    "negative guard",
			raw:     `{"continueMode":"idle_only","onTurnFinished":"evaluate","onAsyncCompleted":"evaluate","maxAutonomousTurns":-1}`,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := DecodeControllerSpec(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.wantNil {
				require.Nil(t, spec)
				return
			}
			require.NotNil(t, spec)
		})
	}
}

func TestControllerSpec_EncodeDecodeRoundTrip(t *testing.T) {
	original := &ControllerSpec{
		ContinueMode:             ContinueModeIdleOnly,
		OnTurnFinished:           TurnPolicyEvaluate,
		OnAsyncCompleted:         AsyncPolicyWait,
		MaxAutonomousTurns:       intPtr(5),
		MaxConsecutiveNoProgress: intPtr(2),
	}
	encoded, err := original.Encode()
	require.NoError(t, err)

	decoded, err := DecodeControllerSpec(encoded)
	require.NoError(t, err)
	require.Equal(t, original, decoded)
}

func TestControllerSpec_EncodeRejectsInvalid(t *testing.T) {
	_, err := (&ControllerSpec{ContinueMode: "bogus"}).Encode()
	require.Error(t, err)
}
