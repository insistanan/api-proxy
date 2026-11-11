package sensitive

import (
	"regexp"
	"strings"

	"github.com/insistanan/api-proxy/internal/config"
)

type commandRule struct {
	name     string
	regex    *regexp.Regexp
	keywords []string
}

// mayMatch 用规则必须包含的 ASCII 关键词预筛，避免无关脚本逐条运行正则。
func (r commandRule) mayMatch(lower string) bool {
	for _, keyword := range r.keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return len(r.keywords) == 0
}

var builtinCommandRules = []commandRule{
	{
		name:     config.DangerousCmdRuleDestructive,
		keywords: []string{"rm", "dd", "mkfs", "wipefs", "shred", "remove-item", "ri", "rd", "del", "erase"},
		regex:    regexp.MustCompile(`(?im)\brm\s+(?:-[a-z]*[rR][a-z]*|--recursive)(?:\s+--?[a-z-]+)*\s+["']?(?:/(?:\*|etc\b|usr\b|home\b|root\b|var\b)?|~/?|\$\{?HOME\}?/?)["']?(?:[ \t]*(?:$|[;&|])|[\\/])|\bdd\b[^\r\n]*of\s*=\s*/dev/(?:sd[a-z][0-9]*|hd[a-z][0-9]*|vd[a-z][0-9]*|xvd[a-z][0-9]*|nvme[0-9]+n[0-9]+(?:p[0-9]+)?|mmcblk[0-9]+(?:p[0-9]+)?|r?disk[0-9]+(?:s[0-9]+)?)\b|\bmkfs(?:\.[a-z0-9]+)?\b|\b(?:wipefs|shred)\b[^\r\n]*/dev/|\b(?:Remove-Item|ri|rd|rmdir|del|erase)\b[^\r\n]*(?:-[a-z]*recurse\b|/s\b)[^\r\n]*["']?[a-z]:[\\/](?:["'\s;]|\*|Windows\b|Users\b)|\b(?:Remove-Item|ri|rd|rmdir|del|erase)\s+[^\r\n]*[a-z]:[\\/](?:["'\s;]|\*|Windows\b|Users\b)[^\r\n]*(?:-recurse\b|/s\b)`),
	},
	{
		name:     config.DangerousCmdRuleDownloadExecute,
		keywords: []string{"curl", "wget", "invoke-webrequest", "invoke-restmethod", "iwr", "irm"},
		regex:    regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^\r\n|]*\|\s*(?:(?:sudo|env)\s+)?(?:/bin/|/usr/bin/)?(?:bash|sh|zsh)\b|\b(?:Invoke-WebRequest|Invoke-RestMethod|iwr|irm)\b[^\r\n|]*\|\s*(?:Invoke-Expression|iex)\b`),
	},
	{
		name:     config.DangerousCmdRuleReverseShell,
		keywords: []string{"nc", "bash"},
		regex:    regexp.MustCompile(`(?i)\bnc\s+-e\s+(?:/bin/)?(?:ba)?sh\b|\bbash\s+-i\s+>&?\s*/dev/tcp/`),
	},
	{
		name:     config.DangerousCmdRulePrivilegeEscalation,
		keywords: []string{"chmod", "sudo"},
		regex:    regexp.MustCompile(`(?i)\bchmod\s+777\s+/etc\b|\bsudo\s+su(?:\s|$)`),
	},
	{
		name:     config.DangerousCmdRuleEnvironmentTampering,
		keywords: []string{"ld_preload", "path"},
		regex:    regexp.MustCompile(`(?i)\bLD_PRELOAD\s*=\s*["']?(?:/tmp/|/var/tmp/|/dev/shm/|\./)[^\s;"']+|\bPATH\s*=\s*["']?(?:\.|/tmp(?:/[^:\s;"']*)?|/var/tmp(?:/[^:\s;"']*)?|/dev/shm(?:/[^:\s;"']*)?)(?:[:\s;"']|$)`),
	},
}
