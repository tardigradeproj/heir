package cleanup

import "testing"

func testCleanupHelper(clean, cleanAdd *bool, release bool) func() {
	cu := Make(func() {
		*clean = true
	})
	cu.Add(func() {
		*cleanAdd = true
	})
	defer cu.Clean()
	if release {
		return cu.Release()
	}
	return nil
}

func TestCleanup(t *testing.T) {
	clean := false
	cleanAdd := false
	testCleanupHelper(&clean, &cleanAdd, false)
	if !clean {
		t.Fatalf("cleanup function was not called.")
	}
	if !cleanAdd {
		t.Fatalf("added cleanup function was not called.")
	}
}

func TestRelease(t *testing.T) {
	clean := false
	cleanAdd := false
	cleaner := testCleanupHelper(&clean, &cleanAdd, true)

	// Check that clean was not called after release.
	if clean {
		t.Fatalf("cleanup function was called.")
	}
	if cleanAdd {
		t.Fatalf("added cleanup function was called.")
	}

	// Call the cleaner function and check that both cleanup functions are called.
	cleaner()
	if !clean {
		t.Fatalf("cleanup function was not called.")
	}
	if !cleanAdd {
		t.Fatalf("added cleanup function was not called.")
	}
}
