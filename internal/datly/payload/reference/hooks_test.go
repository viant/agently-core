package reference

import (
	"bytes"
	"compress/gzip"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPayloadReadPreservesExactBinaryAndWhitespace(t *testing.T) {
	for _, original := range [][]byte{[]byte("  {\"number\":1} \n"), {0xff, 0, 1, 2, 0x20, 0x0a}, []byte(" text receipt\n\n")} {
		for _, compression := range []string{"none", "gzip"} {
			body := bytes.Clone(original)
			if compression == "gzip" {
				var output bytes.Buffer
				writer := gzip.NewWriter(&output)
				_, err := writer.Write(body)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				body = output.Bytes()
			}
			payload := &PayloadView{Id: "exact", Compression: compression, InlineBody: &body}
			require.NoError(t, payload.OnFetch(context.Background()))
			require.Equal(t, original, *payload.InlineBody)
			require.NoError(t, payload.OnFetch(context.Background()))
			require.Equal(t, original, *payload.InlineBody)
		}
	}
	payload := &PayloadView{Compression: "gzip"}
	require.NoError(t, payload.OnFetch(context.Background()))
	require.Nil(t, payload.InlineBody)
	empty := []byte{}
	payload = &PayloadView{Compression: "none", InlineBody: &empty}
	require.NoError(t, payload.OnFetch(context.Background()))
	require.NotNil(t, payload.InlineBody)
	require.Empty(t, *payload.InlineBody)
}
func TestPayloadReadRejectsCorruptGzipWithoutPartialMutation(t *testing.T) {
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	_, err := writer.Write([]byte("original text"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	valid := output.Bytes()
	badChecksum := bytes.Clone(valid)
	badChecksum[len(badChecksum)-8] ^= 1
	for _, body := range [][]byte{[]byte("not gzip"), valid[:len(valid)-3], badChecksum} {
		original := bytes.Clone(body)
		payload := &PayloadView{Id: "bad", Compression: "gzip", InlineBody: &body}
		require.ErrorContains(t, payload.OnFetch(context.Background()), "decode gzip payload")
		require.Equal(t, "gzip", payload.Compression)
		require.Equal(t, original, *payload.InlineBody)
	}
}
