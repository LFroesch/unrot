package state

import "testing"

func TestChallengeDiffJSONUnmarshalLegacyValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "number", raw: `2`, want: 2},
		{name: "numeric string", raw: `"3"`, want: 3},
		{name: "basic string", raw: `"basic"`, want: 1},
		{name: "advanced alias", raw: `"hard"`, want: 3},
		{name: "adaptive alias", raw: `"default"`, want: 0},
		{name: "out of range number clamps", raw: `9`, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got challengeDiffJSON
			if err := got.UnmarshalJSON([]byte(tt.raw)); err != nil {
				t.Fatalf("UnmarshalJSON(%s) error = %v", tt.raw, err)
			}
			if int(got) != tt.want {
				t.Fatalf("UnmarshalJSON(%s) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestChallengeDiffJSONRejectsUnknownString(t *testing.T) {
	var got challengeDiffJSON
	if err := got.UnmarshalJSON([]byte(`"mystery"`)); err == nil {
		t.Fatal("expected unknown string to fail")
	}
}
