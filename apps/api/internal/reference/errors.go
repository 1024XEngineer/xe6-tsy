package reference

import "errors"

// ErrNotImplemented marks module six service methods that are wired but do not
// yet execute business logic or persistence.
var ErrNotImplemented = errors.New("reference module service is not implemented")
