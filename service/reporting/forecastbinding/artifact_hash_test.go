package forecastbinding

import "testing"

func TestArtifactHashAcceptsEncoderSpellingWithoutRoundingAwayChanges(t *testing.T) {
	first, err := ArtifactHash([]byte(`{"b":"é","a":[1,0.0000001,-0]}`))
	if err != nil {
		t.Fatal(err)
	}
	same, err := ArtifactHash([]byte(`{"a":[1.0,1e-7,0],"b":"\u00e9"}`))
	if err != nil || same != first {
		t.Fatal("encoder-only change", err)
	}
	large, _ := ArtifactHash([]byte(`{"value":9007199254740992}`))
	changed, _ := ArtifactHash([]byte(`{"value":9007199254740993}`))
	if large == changed {
		t.Fatal("large integer tampering rounded away")
	}
	text, _ := ArtifactHash([]byte(`{"value":"9007199254740992"}`))
	if text == large {
		t.Fatal("string-number collision")
	}
	if _, err = ArtifactHash([]byte(`1e1000000000`)); err != nil {
		t.Fatal("symbolic exponent", err)
	}
	if _, err = ArtifactHash([]byte(`NaN`)); err == nil {
		t.Fatal("non JSON accepted")
	}
}
