package filesystem

import "testing"

func TestContextRequiredError(t *testing.T) {
	t.Parallel()

	if ErrContextRequired.Error() != "filesystem: context required" {
		t.Fatalf("ErrContextRequired = %q", ErrContextRequired)
	}
}
