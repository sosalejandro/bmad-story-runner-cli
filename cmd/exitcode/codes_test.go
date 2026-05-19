package exitcode

import "testing"

// TestCodeInt locks in the integer values of every code. These are part of
// the CLI's public contract — changing any of them is a breaking change.
func TestCodeInt(t *testing.T) {
	tests := []struct {
		code Code
		want int
	}{
		{Success, 0},
		{UserError, 1},
		{NoResult, 2},
		{ArgsError, 10},
		{SystemError, 20},
		{ValidationError, 30},
		{NotFound, 40},
		{Conflict, 50},
	}
	for _, tt := range tests {
		if got := tt.code.Int(); got != tt.want {
			t.Errorf("%s.Int() = %d, want %d", tt.code, got, tt.want)
		}
	}
}

func TestCodeString(t *testing.T) {
	tests := []struct {
		code Code
		want string
	}{
		{Success, "SUCCESS"},
		{UserError, "USER_ERROR"},
		{NoResult, "NO_RESULT"},
		{ArgsError, "ARGS_ERROR"},
		{SystemError, "SYSTEM_ERROR"},
		{ValidationError, "VALIDATION_ERROR"},
		{NotFound, "NOT_FOUND"},
		{Conflict, "CONFLICT"},
		{Code(999), "UNKNOWN"},
	}
	for _, tt := range tests {
		if got := tt.code.String(); got != tt.want {
			t.Errorf("Code(%d).String() = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestAllReturnsEveryDefinedCode(t *testing.T) {
	got := All()
	if len(got) != 8 {
		t.Fatalf("All() returned %d codes; expected 8 (update both All() and this test when adding a code)", len(got))
	}
}

func TestDescribeHasNoUnknown(t *testing.T) {
	for _, c := range All() {
		desc := Describe(c)
		if desc == "" || desc == "unknown exit code" {
			t.Errorf("Describe(%s) = %q; every code in All() must have a real description", c, desc)
		}
	}
}
