package service

import (
	"encoding/json"
	"testing"
)

func TestIsJSONObject(t *testing.T) {
	cases := map[string]bool{`{}`: true, `{"hidden":["/a"]}`: true, `[]`: false, `null`: false, `"x"`: false, `{`: false}
	for in, want := range cases {
		if got := isJSONObject([]byte(in)); got != want {
			t.Fatalf("isJSONObject(%s)=%v want %v", in, got, want)
		}
	}
	if !isJSONObject(json.RawMessage(` {"a":1} `)) {
		t.Fatal("whitespace should be tolerated")
	}
}
