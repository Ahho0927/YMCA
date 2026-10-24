package lms

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
)

// requestError deliberately never formats the underlying error. net/http errors
// often include complete redirect URLs with SID, SAML and OpenAM transaction data.
type requestError struct {
	method, host, reason string
	cause                error
}

func (e *requestError) Error() string {
	return fmt.Sprintf("%s %s: %s", e.method, e.host, e.reason)
}
func (e *requestError) Unwrap() error { return e.cause }

func requestFailure(req *http.Request, err error) error {
	reason := "network request failed (check connectivity and retry)"
	var dns *net.DNSError
	var timeout net.Error
	var certificate *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var wrongHost x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	switch {
	case errors.Is(err, errUntrustedTarget):
		reason = "redirect blocked: destination has an untrusted host, scheme or port"
	case errors.Is(err, errRedirectLimit):
		reason = "too many redirects"
	case errors.Is(err, context.Canceled):
		reason = "request canceled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "request timed out"
	case errors.As(err, &dns):
		reason = "DNS lookup failed"
	case errors.As(err, &certificate), errors.As(err, &unknownCA), errors.As(err, &wrongHost), errors.As(err, &invalidCertificate):
		reason = "TLS certificate verification failed"
	case errors.As(err, &timeout) && timeout.Timeout():
		reason = "request timed out"
	case errors.Is(err, syscall.ECONNREFUSED):
		reason = "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		reason = "connection reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		reason = "connection closed before the response completed"
	}
	return &requestError{method: req.Method, host: req.URL.Hostname(), reason: reason, cause: err}
}
