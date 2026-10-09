package httpx

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"

	"github.com/herlenashavi/go-api/pkg/problem"
)

var (
	validatorOnce sync.Once
	validatorInst *validator.Validate
)

// Validator returns the shared validator instance, built on first use.
// Field names in error messages come from each field's `json` tag (falling
// back to the Go field name) so a validation error names the field exactly
// as the client sent it, not as it's spelled in your Go struct.
func Validator() *validator.Validate {
	validatorOnce.Do(func() {
		validatorInst = validator.New(validator.WithRequiredStructEnabled())
		validatorInst.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if name == "" || name == "-" {
				return fld.Name
			}
			return name
		})
	})
	return validatorInst
}

// Validate runs struct tag validation (`validate:"..."`, per
// go-playground/validator) on in and maps any failure to a 422
// problem+json with one FieldError per violated rule.
func Validate(in any) error {
	if err := Validator().Struct(in); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			fields := make([]problem.FieldError, 0, len(verrs))
			for _, fe := range verrs {
				fields = append(fields, problem.FieldError{
					Field:  fe.Field(),
					Detail: validationMessage(fe),
				})
			}
			return problem.Validation(fields...)
		}
		// Not a ValidationErrors (e.g. a struct-level usage error) — treat
		// as a 400 rather than silently passing validation.
		return problem.BadRequest(err.Error())
	}
	return nil
}

func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "min":
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s", fe.Param())
	case "email":
		return "must be a valid email address"
	case "uuid", "uuid4":
		return "must be a valid UUID"
	case "oneof":
		return fmt.Sprintf("must be one of: %s", fe.Param())
	default:
		return fmt.Sprintf("failed validation %q", fe.Tag())
	}
}
