package sensitive

import (
	"regexp"

	"github.com/BenedictKing/api-proxy/internal/config"
)

type commandRule struct {
	name  string
	regex *regexp.Regexp
}

var builtinCommandRules = []commandRule{
	{
		name:  config.DangerousCmdRuleDestructive,
		regex: regexp.MustCompile(`(?im)\brm\s+(?:-[a-z]*[rR][a-z]*|--recursive)(?:\s+--?[a-z-]+)*\s+["']?(?:/(?:\*|etc\b|usr\b|home\b|root\b|var\b)?|~/?|\$\{?HOME\}?/?)["']?(?:[ \t]*(?:$|[;&|])|[\\/])|\bdd\b[^\r\n]*(?:if\s*=\s*/dev/(?:zero|random|urandom)\b|of\s*=\s*/dev/)|\bmkfs(?:\.[a-z0-9]+)?\b|\b(?:wipefs|shred)\b[^\r\n]*/dev/|\b(?:Remove-Item|ri|rd|rmdir|del|erase)\b[^\r\n]*(?:-[a-z]*recurse\b|/s\b)[^\r\n]*["']?[a-z]:[\\/](?:["'\s;]|\*|Windows\b|Users\b)|\b(?:Remove-Item|ri|rd|rmdir|del|erase)\s+[^\r\n]*[a-z]:[\\/](?:["'\s;]|\*|Windows\b|Users\b)[^\r\n]*(?:-recurse\b|/s\b)`),
	},
	{
		name:  config.DangerousCmdRuleDownloadExecute,
		regex: regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^\r\n|]*\|\s*(?:(?:sudo|env)\s+)?(?:/bin/|/usr/bin/)?(?:bash|sh|zsh)\b|\b(?:Invoke-WebRequest|Invoke-RestMethod|iwr|irm)\b[^\r\n|]*\|\s*(?:Invoke-Expression|iex)\b`),
	},
	{
		name:  config.DangerousCmdRuleReverseShell,
		regex: regexp.MustCompile(`(?i)\bnc\s+-e\s+(?:/bin/)?(?:ba)?sh\b|\bbash\s+-i\s+>&?\s*/dev/tcp/`),
	},
	{
		name:  config.DangerousCmdRulePrivilegeEscalation,
		regex: regexp.MustCompile(`(?i)\bchmod\s+777\s+/etc\b|\bsudo\s+su(?:\s|$)`),
	},
	{
		name:  config.DangerousCmdRuleEnvironmentTampering,
		regex: regexp.MustCompile(`(?i)\bLD_PRELOAD\s*=\s*[^\s;]+|\bPATH\s*=\s*["']?(?:\.|/tmp(?:/[^:\s;"']*)?|/var/tmp(?:/[^:\s;"']*)?|/dev/shm(?:/[^:\s;"']*)?)(?:[:\s;"']|$)`),
	},
}
