package types

import (
	"bytes"
	"fmt"
)

func NewMultiError(errs []error) *MultiError {
	return &MultiError{errs: errs}
}

type MultiError struct {
	errs []error
}

func (m MultiError) Error() string {
	buf := &bytes.Buffer{}
	for _, err := range m.errs {
		_, _ = fmt.Fprintf(buf, "%v\n", err.Error())
	}
	return buf.String()
}
