package store

// Internals for the external tests in package store_test (which can import
// testdb; package store's own tests can't, as testdb imports store).
var (
	Hash        = hash
	IsDataError = isDataError
	MergeRange  = mergeRange
	Watermark   = watermark
)

// SetApplyHook installs a failure injector before each group's bulk
// statement; nil removes it.
func SetApplyHook(h func(group string) error) { applyHook = h }
