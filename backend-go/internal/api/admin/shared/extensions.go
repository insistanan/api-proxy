package shared

import "insistanan/api-proxy/internal/clients"

// WithExtensionsLocks 串行化读取或修改共享 Skills 与 MCP 文件的操作。
func WithExtensionsLocks(skillsAPI *clients.SkillsAPI, mcpAPI *clients.MCPAPI, fn func() error) error {
	if skillsAPI != nil {
		return skillsAPI.WithLock(func() error {
			if mcpAPI != nil {
				return mcpAPI.WithLock(fn)
			}
			return fn()
		})
	}
	if mcpAPI != nil {
		return mcpAPI.WithLock(fn)
	}
	return fn()
}
