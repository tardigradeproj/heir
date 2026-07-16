// Package cleanup provides utilities to clean "stuff" on defers.
package cleanup

// Cleanup allows defers to be aborted when cleanup needs to happen
// conditionally. Usage:
//
//		 cu := cleanup.Make(func() { f.Close() })
//		 defer cu.Clean() // failure before release is called will close the file.
//		 ...
//	   cu.Add(func() { f2.Close() })  // Adds another cleanup function
//	   ...
//		 cu.Release() // on success, aborts closing the file.
//		 return f
type Cleanup struct {
	cleaners []func()
}

// Make creates a new Cleanup object.
func Make(f func()) Cleanup {
	return Cleanup{cleaners: []func(){f}}
}

// Add adds a new function to be called on Clean().
func (c *Cleanup) Add(f func()) {
	c.cleaners = append(c.cleaners, f)
}

// Clean calls all cleanup functions in reverse order.
func (c *Cleanup) Clean() {
	clean(c.cleaners)
	c.cleaners = nil
}

// Release releases the cleanup from its duties, i.e. cleanup functions are not
// called after this point. Returns a function that calls all registered
// functions in case the caller has use for them.
func (c *Cleanup) Release() func() {
	old := c.cleaners
	c.cleaners = nil
	return func() { clean(old) }
}

func clean(cleaners []func()) {
	for i := len(cleaners) - 1; i >= 0; i-- {
		cleaners[i]()
	}
}
