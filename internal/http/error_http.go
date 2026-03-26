package http

import (
	"net/http"

	reederrors "github.com/sjzar/reed/internal/errors"
)

// CodeToHTTPStatus maps domain error codes to HTTP status codes.
func CodeToHTTPStatus(code reederrors.Code) int {
	switch code {
	case reederrors.CodeNotFound:
		return http.StatusNotFound
	case reederrors.CodeValidation, reederrors.CodeInvalidArg:
		return http.StatusBadRequest
	case reederrors.CodeConflict, reederrors.CodeAlreadyExists:
		return http.StatusConflict
	case reederrors.CodePermission:
		return http.StatusForbidden
	case reederrors.CodeUnavailable:
		return http.StatusServiceUnavailable
	case reederrors.CodeTimeout:
		return http.StatusGatewayTimeout
	case reederrors.CodeCanceled:
		return 499 // client closed request
	default:
		return http.StatusInternalServerError
	}
}

// HTTPStatus returns the HTTP status code for the given error.
func HTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return CodeToHTTPStatus(reederrors.GetCode(err))
}
