package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/storage/logger"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
)

func DecodeEmbeddings(body []byte, count int) ([][]float64, error) {
	var response struct {
		Data []struct {
			Index     *int      `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("向量响应不是有效 JSON: %w", err)
	}
	if len(response.Data) != count {
		return nil, fmt.Errorf("向量数量不匹配，期望 %d，得到 %d", count, len(response.Data))
	}
	vectors := make([][]float64, count)
	dim := 0
	for _, item := range response.Data {
		if item.Index == nil || *item.Index < 0 || *item.Index >= count || vectors[*item.Index] != nil {
			return nil, fmt.Errorf("向量索引越界或重复")
		}
		if dim == 0 {
			dim = len(item.Embedding)
		}
		if dim == 0 || dim > 4096 || len(item.Embedding) != dim {
			return nil, fmt.Errorf("向量维度为空、超限或不一致")
		}
		norm := 0.0
		for _, v := range item.Embedding {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("向量包含非有限数值")
			}
			norm += v * v
		}
		if norm == 0 || math.IsInf(norm, 0) {
			return nil, fmt.Errorf("向量范数无效")
		}
		norm = math.Sqrt(norm)
		for i := range item.Embedding {
			item.Embedding[i] /= norm
		}
		vectors[*item.Index] = item.Embedding
	}
	return vectors, nil
}

func (s *Service) embeddingUpstream(cfg config.IntentRoutingConfig) (*config.UpstreamConfig, int, error) {
	if cfg.EmbeddingChannel == "" {
		return nil, 0, fmt.Errorf("请指定用于辅助向量计算的 Embeddings 渠道")
	}
	for i, up := range s.Cfg.GetConfig().EmbeddingsUpstream {
		if up.ID != cfg.EmbeddingChannel {
			continue
		}
		if up.Status != "" && up.Status != "active" {
			return nil, 0, fmt.Errorf("向量渠道未启用")
		}
		if len(up.APIKeys) == 0 {
			return nil, 0, fmt.Errorf("向量渠道未配置密钥")
		}
		return up.Clone(), i, nil
	}
	return nil, 0, fmt.Errorf("向量渠道不存在")
}

func (s *Service) embeddingSpace(cfg config.IntentRoutingConfig) (string, error) {
	if cfg.EmbeddingSource == "local" {
		if s.Local == nil {
			return "", fmt.Errorf("本地向量助手未装配")
		}
		if cfg.EmbeddingModel != s.Local.Status().Manifest.ModelName {
			return "", fmt.Errorf("语义配置的模型名称与本地助手不一致")
		}
		return s.Local.Space()
	}
	up, _, err := s.embeddingUpstream(cfg)
	if err != nil {
		return "", err
	}
	return digest(fmt.Sprintf("%s|%s|%s|%s|%t|%d", up.ID, up.ServiceType, up.GetEffectiveBaseURL(), config.ResolveUpstreamModel(cfg.EmbeddingModel, up), up.UseFullURL, up.EmbeddingsDimensions)), nil
}

func (s *Service) embed(ctx context.Context, cfg config.IntentRoutingConfig, inputs []string, parent string) (vectors [][]float64, err error) {
	if cfg.EmbeddingSource == "local" {
		if s.Local == nil {
			return nil, fmt.Errorf("本地向量助手未装配")
		}
		return s.Local.Embed(ctx, inputs)
	}
	if s.Caller == nil || s.Caller.ChannelScheduler == nil {
		return nil, fmt.Errorf("向量调用未绑定渠道调度器")
	}
	up, channelIndex, err := s.embeddingUpstream(cfg)
	if err != nil {
		return nil, err
	}
	selection, err := s.Caller.ChannelScheduler.SelectMappedChannel(scheduler.ChannelKindEmbeddings, up.ID)
	if err != nil {
		return nil, err
	}
	defer selection.Permit.Release()
	up = selection.Upstream
	up.BaseURL = up.GetEffectiveBaseURL()
	model := config.ResolveUpstreamModel(cfg.EmbeddingModel, up)
	if model == "" {
		return nil, fmt.Errorf("向量模型为空")
	}
	key, err := s.Cfg.GetNextAPIKey(up, map[string]bool{}, "Intent-Embedding")
	if err != nil {
		return nil, err
	}
	proxyURL, err := s.Cfg.ResolveUpstreamProxyURL(up)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"model": model, "input": inputs, "encoding_format": "float"}
	if up.EmbeddingsDimensions > 0 {
		payload["dimensions"] = up.EmbeddingsDimensions
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	ctx = upstreamcontrol.WithScope(ctx)
	ctx = s.Cfg.UpstreamRequestContext(ctx, up, key)
	requestID := "intent-embedding-" + logger.NewRequestID()
	started := time.Now()
	promptTokens := 0
	defer func() {
		if s.Caller.RequestLogStore == nil {
			return
		}
		status, message := "success", ""
		if err != nil {
			status, message = "failed", err.Error()
		}
		s.Caller.RequestLogStore.Record(metrics.RequestLogEntry{RequestID: requestID, AttemptID: requestID, ParentRequestID: parent,
			APIType: "intent_embedding", Model: model, ResolvedModel: model, InputTokens: promptTokens, ChannelIndex: channelIndex, ChannelName: up.Name, Status: status, ErrorMessage: message, DurationMs: time.Since(started).Milliseconds()})
	}()
	url := utils.BuildUpstreamURL(up.GetEffectiveBaseURL(), "/v1", "/embeddings", up.UseFullURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	utils.SetAuthenticationHeader(req.Header, key)
	response, err := httpclient.Send(req, httpclient.SendOptions{Timeout: time.Duration(cfg.TimeoutMS) * time.Millisecond, ProxyURL: proxyURL, InsecureSkipVerify: up.InsecureSkipVerify})
	if err != nil {
		if ctx.Err() != nil || upstreamcontrol.IsLocalError(err) {
			selection.Permit.RecordHealthNeutral()
		} else {
			selection.Permit.RecordHealthFailure()
		}
		return nil, fmt.Errorf("向量调用失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 500 {
			selection.Permit.RecordHealthFailure()
		} else {
			selection.Permit.RecordHealthNeutral()
		}
		return nil, fmt.Errorf("向量上游返回 HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("读取向量失败: %w", err)
	}
	if len(data) > 32<<20 {
		return nil, fmt.Errorf("向量响应超过 32 MiB")
	}
	var metadata struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("向量响应无效: %w", err)
	}
	if metadata.Model != "" && metadata.Model != model {
		return nil, fmt.Errorf("向量上游返回的模型 %q 与配置的 %q 不一致，请使用实际模型名重建索引", metadata.Model, model)
	}
	promptTokens = metadata.Usage.PromptTokens
	vectors, err = DecodeEmbeddings(data, len(inputs))
	if err != nil {
		selection.Permit.RecordHealthNeutral()
		return nil, err
	}
	if up.EmbeddingsDimensions > 0 && len(vectors[0]) != up.EmbeddingsDimensions {
		selection.Permit.RecordHealthNeutral()
		return nil, fmt.Errorf("上游没有返回所配置的向量维度")
	}
	selection.Permit.RecordHealthSuccess()
	return vectors, nil
}
