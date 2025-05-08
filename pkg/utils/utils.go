package utils

import (
	"os"
	"log/slog"
	"regexp"
)

func Fatal(err error, l *slog.Logger, message string, args ...any) {
	if err != nil {
		l.Error(message, append([]any{"error", err}, args...)...)
		os.Exit(1)
	}
}

// https://ihateregex.io/expr/ip/
func IsValidIPv4(ip string) bool {
	var regex = regexp.MustCompile(`(\b25[0-5]|\b2[0-4][0-9]|\b[01]?[0-9][0-9]?)(\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)){3}`)
	return regex.MatchString(ip)
}
