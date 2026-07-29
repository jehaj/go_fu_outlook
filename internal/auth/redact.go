package auth

import (
	"regexp"
)

var (
	authHeaderRegex   = regexp.MustCompile(`(?i)(Authorization:\s*)[^\r\n]+`)
	jsonTokenRegex    = regexp.MustCompile(`(?i)("(?:access_token|refresh_token|id_token|secret|password)"\s*:\s*")([^"]+)(")`)
	kvTokenRegex      = regexp.MustCompile(`(?i)((?:token|secret|password)=)([^\s&]+)`)
)

// Redact sanitizes log strings, replacing sensitive tokens and headers with [REDACTED].
func Redact(input string) string {
	if input == "" {
		return ""
	}

	result := authHeaderRegex.ReplaceAllString(input, "${1}[REDACTED]")
	result = jsonTokenRegex.ReplaceAllString(result, "${1}[REDACTED]${3}")
	result = kvTokenRegex.ReplaceAllString(result, "${1}[REDACTED]")

	return result
}
