package spec

import "errors"

func asValidationErrors(err error, target *ValidationErrors) bool {
	return errors.As(err, target)
}
