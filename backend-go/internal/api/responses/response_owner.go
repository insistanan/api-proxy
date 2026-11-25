package responses

import (
	"net/http"

	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

func responseUpstreamOwner(resp *http.Response) string {
	if resp == nil || resp.Request == nil {
		return ""
	}
	candidate, bound := upstreamcontrol.CandidateFromContext(resp.Request.Context())
	if !bound {
		return ""
	}
	return candidate.Identity.Fingerprint()
}
