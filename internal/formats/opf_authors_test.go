package formats

import (
	"reflect"
	"testing"
)

func TestSplitAuthorsStraySeparators(t *testing.T) {
	for in, want := range map[string][]string{
		"Andy Weir;":                    {"Andy Weir"},
		"A. Author; B. Writer":          {"A. Author", "B. Writer"},
		"Terry Pratchett & Neil Gaiman": {"Terry Pratchett", "Neil Gaiman"},
		"Ivo Andrić,":                   {"Ivo Andrić"},
	} {
		if got := splitAuthors(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q", in, got)
		}
	}
}
