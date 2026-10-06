package invariant

import (
	"github.com/stretchr/testify/require"
	"testing"
)

type protocolMarkers struct{ ProtocolRevision, ProtocolOnly, ProtocolThreadId bool }
type protocolNativeRow struct {
	ProtocolRevision *int
	ProtocolOnly     *int
	ProtocolThreadId *string
	Has              *protocolMarkers
}

func TestNativeProtocolFieldsRespectSuppliednessAndOwnership(t *testing.T) {
	zero, one := 0, 1
	key := "wire"
	row := &protocolNativeRow{ProtocolRevision: &one, ProtocolOnly: &one, ProtocolThreadId: &key, Has: &protocolMarkers{}}
	require.NoError(t, ValidateNativeProtocolFields(row, true), "hydrated protocol values are not supplied mutations")
	row.Has.ProtocolThreadId = true
	require.Error(t, ValidateNativeProtocolFields(row, true))
	require.Equal(t, key, *row.ProtocolThreadId)
	row.ProtocolThreadId = nil
	require.Error(t, ValidateNativeProtocolFields(row, true), "explicit null cannot clear a bound identity")
	row = &protocolNativeRow{ProtocolRevision: &zero, ProtocolOnly: &zero, Has: &protocolMarkers{ProtocolRevision: true, ProtocolOnly: true}}
	require.NoError(t, ValidateNativeProtocolFields(row, false))
	row.ProtocolOnly = &one
	require.Error(t, ValidateNativeProtocolFields(row, false))
	row = &protocolNativeRow{ProtocolThreadId: &key, Has: &protocolMarkers{ProtocolThreadId: true}}
	require.Error(t, ValidateNativeProtocolFields(row, false))
}
