package proxycore

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/availability"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/httpclient"
)

const availabilityObservationKey = "__availability_observation"

// 每次构建分配独立观测，兼容重试也不能覆盖上一次 HTTP 失败。
type availabilityObservation struct {
	startedAt  time.Time
	upstream   *config.UpstreamConfig
	apiKey     string
	kind       string
	model      string
	sent       bool
	recorded   bool
	retryAfter string
	status     int
	body       string
	evidence   string
}

func currentAvailabilityObservation(c *gin.Context) *availabilityObservation {
	if c == nil {
		return nil
	}
	value, _ := c.Get(availabilityObservationKey)
	observation, _ := value.(*availabilityObservation)
	return observation
}

func (a UpstreamAttempt) beginAvailabilityObservation(upstream *config.UpstreamConfig, apiKey string) {
	if previous := currentAvailabilityObservation(a.Context); previous != nil && !previous.recorded && previous.status != 0 {
		recordAttemptLog(a.Context, a.LogContext, previous.upstream, a.APIType, BindRequestLogID(a.Context),
			previous.upstream.BaseURL, previous.apiKey, "failed", previous.status, false, previous.startedAt,
			"upstream", previous.body, true, a.IsStream, nil)
	}
	model := config.ResolveUpstreamModel(a.LogContext.Model, upstream)
	if string(a.Kind) == "images" && upstream.ServiceType == "responses" {
		model = upstream.ResponsesModel
	}
	observation := &availabilityObservation{startedAt: time.Now(), upstream: upstream, apiKey: apiKey, kind: string(a.Kind), model: model}
	a.Context.Set(availabilityObservationKey, observation)
	ResetRequestLogFirstToken(a.Context)
}

// 只绑定最终出站请求，避免准备阶段的视觉辅助调用被当成本候选已经发送。
func (a UpstreamAttempt) observeAvailabilityRequest(req *http.Request) *http.Request {
	observation := currentAvailabilityObservation(a.Context)
	if observation == nil {
		return req
	}
	return req.WithContext(httpclient.WithSendObserver(req.Context(), func() { observation.sent = true }))
}

func (a UpstreamAttempt) observeAvailabilityResponse(resp *http.Response) {
	if observation := currentAvailabilityObservation(a.Context); observation != nil {
		if resp != nil {
			observation.retryAfter = resp.Header.Get("Retry-After")
		}
	}
}

func (a UpstreamAttempt) rememberAvailabilityFailure(resp *http.Response, body []byte) {
	if observation := currentAvailabilityObservation(a.Context); observation != nil {
		observation.status = resp.StatusCode
		observation.body = string(body)
	}
}

func availabilitySample(c *gin.Context, success bool, status int, errorType, message string) *availability.Sample {
	observation := currentAvailabilityObservation(c)
	if observation == nil || !observation.sent || observation.upstream.ID == "" || observation.model == "" {
		return nil
	}
	identity := observation.upstream.RequestIdentity(observation.apiKey)
	if observation.evidence != "" {
		message = observation.evidence
	}
	outcome, reason := availability.Classify(success, status, errorType, message)
	sample := &availability.Sample{Kind: observation.kind, ChannelID: observation.upstream.ID, Model: observation.model, CandidateID: identity.Fingerprint(), Outcome: outcome, Reason: reason}
	if outcome == availability.RateLimited {
		sample.CooldownUntil = availability.Cooldown(observation.retryAfter, time.Now())
	}
	return sample
}
