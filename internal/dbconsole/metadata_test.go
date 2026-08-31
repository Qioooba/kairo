package dbconsole

import "testing"

func TestObjectCategory(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"TABLE":             "tables",
		"base table":        "tables",
		"VIEW":              "views",
		"MATERIALIZED VIEW": "views",
		"FUNCTION":          "functions",
		"PROCEDURE":         "procedures",
		"PACKAGE":           "procedures",
		"TRIGGER":           "triggers",
		"SEQUENCE":          "other",
	}
	for objectType, want := range tests {
		objectType, want := objectType, want
		t.Run(objectType, func(t *testing.T) {
			t.Parallel()
			if got := objectCategory(objectType); got != want {
				t.Fatalf("objectCategory(%q) = %q, want %q", objectType, got, want)
			}
		})
	}
}
