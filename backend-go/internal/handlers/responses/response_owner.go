package responses

import (
	"net/http"

	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
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
