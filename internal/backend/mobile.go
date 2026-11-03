package backend

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
)

// ListProviderInfo returns a secret-free view of every provider the
// workspace knows: the catalog merged with its configured providers.
func (b *Backend) ListProviderInfo(workspaceID string) ([]proto.ProviderInfo, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	cfg := ws.Cfg.Config()
	catalog, _ := config.Providers(cfg)
	return providerInfos(cfg, catalog), nil
}

func providerInfos(cfg *config.Config, catalog []catwalk.Provider) []proto.ProviderInfo {
	byID := map[string]*proto.ProviderInfo{}
	var order []string
	for _, p := range catalog {
		id := string(p.ID)
		if _, ok := byID[id]; ok {
			continue
		}
		info := &proto.ProviderInfo{
			ID: id, Name: p.Name, Type: string(p.Type),
			DefaultLargeModelID: p.DefaultLargeModelID, DefaultSmallModelID: p.DefaultSmallModelID,
			Models: modelInfos(p.Models),
		}
		byID[id] = info
		order = append(order, id)
	}
	if cfg != nil && cfg.Providers != nil {
		configured := map[string]config.ProviderConfig{}
		for id, pc := range cfg.Providers.Seq2() {
			configured[id] = pc
		}
		for _, id := range slices.Sorted(maps.Keys(configured)) {
			pc := configured[id]
			info, ok := byID[id]
			if !ok {
				info = &proto.ProviderInfo{ID: id}
				byID[id] = info
				order = append(order, id)
			}
			info.Configured = true
			info.Disabled = pc.Disable
			info.Name = cmp.Or(pc.Name, info.Name, id)
			info.Type = cmp.Or(string(pc.Type), info.Type)
			info.OAuth = pc.OAuthToken != nil && pc.OAuthToken.AccessToken != ""
			info.HasCredential = info.OAuth || strings.TrimSpace(pc.APIKey) != ""
			if len(pc.Models) > 0 {
				info.Models = modelInfos(pc.Models)
			}
		}
	}
	out := make([]proto.ProviderInfo, 0, len(order))
	for _, id := range order {
		info := byID[id]
		if info.Models == nil {
			info.Models = []proto.ProviderModelInfo{}
		}
		out = append(out, *info)
	}
	slices.SortStableFunc(out, func(a, b proto.ProviderInfo) int {
		// Configured providers first; catalog order otherwise.
		switch {
		case a.Configured && !b.Configured:
			return -1
		case b.Configured && !a.Configured:
			return 1
		}
		return 0
	})
	return out
}

func modelInfos(models []catwalk.Model) []proto.ProviderModelInfo {
	out := make([]proto.ProviderModelInfo, 0, len(models))
	for _, m := range models {
		out = append(out, proto.ProviderModelInfo{
			ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow, DefaultMaxTokens: m.DefaultMaxTokens,
			CanReason: m.CanReason, ReasoningLevels: m.ReasoningLevels, DefaultReasoningEffort: m.DefaultReasoningEffort,
			SupportsImages: m.SupportsImages, CostPer1MIn: m.CostPer1MIn, CostPer1MOut: m.CostPer1MOut,
		})
	}
	return out
}

// GetSessionModel returns the model sessionID runs on.
func (b *Backend) GetSessionModel(ctx context.Context, workspaceID, sessionID string) (proto.SessionModel, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.SessionModel{}, err
	}
	sess, err := ws.Sessions.Get(ctx, sessionID)
	if err != nil {
		return proto.SessionModel{}, err
	}
	return sessionModelView(ws.Cfg.Config(), sess.ModelRef), nil
}

func sessionModelView(cfg *config.Config, ref string) proto.SessionModel {
	out := proto.SessionModel{ModelRef: ref, Default: true}
	if cfg == nil {
		return out
	}
	if ref != "" {
		if sel, err := cfg.ResolveModelRef(ref); err == nil {
			out.Large, out.Default = &sel, false
		}
	}
	if out.Large == nil {
		if sel, ok := cfg.Models[config.SelectedModelTypeLarge]; ok {
			out.Large = &sel
		}
	}
	if sel, ok := cfg.Models[config.SelectedModelTypeSmall]; ok {
		out.Small = &sel
	}
	return out
}

// SetSessionModel points sessionID's top-level turns at a model without
// changing the workspace's default. Only the large slot is per-session:
// titles and summaries always use the workspace's small model.
func (b *Backend) SetSessionModel(ctx context.Context, workspaceID, sessionID string, req proto.SessionModelRequest) (proto.SessionModel, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.SessionModel{}, err
	}
	switch req.ModelType {
	case "", config.SelectedModelTypeLarge:
	case config.SelectedModelTypeSmall:
		return proto.SessionModel{}, fmt.Errorf("%w: the small model is workspace-wide; set it with /config/model", ErrInvalidSessionModel)
	default:
		return proto.SessionModel{}, fmt.Errorf("%w: unknown model_type %q", ErrInvalidSessionModel, req.ModelType)
	}
	ref := strings.TrimSpace(req.ModelRef)
	if req.Model != nil {
		if ref != "" {
			return proto.SessionModel{}, fmt.Errorf("%w: give model_ref or model, not both", ErrInvalidSessionModel)
		}
		if req.Model.Provider == "" || req.Model.Model == "" {
			return proto.SessionModel{}, fmt.Errorf("%w: model needs provider and model", ErrInvalidSessionModel)
		}
		ref = req.Model.Provider + "/" + req.Model.Model
	}
	cfg := ws.Cfg.Config()
	if ref != "" {
		if cfg == nil {
			return proto.SessionModel{}, fmt.Errorf("%w: workspace has no config", ErrInvalidSessionModel)
		}
		if _, err := cfg.ResolveModelRef(ref); err != nil {
			return proto.SessionModel{}, fmt.Errorf("%w: %v", ErrInvalidSessionModel, err)
		}
	}
	sess, err := ws.Sessions.Get(ctx, sessionID)
	if err != nil {
		return proto.SessionModel{}, err
	}
	if !b.ownedHere(sess.OwnerNode) {
		return proto.SessionModel{}, fmt.Errorf("%w (%s)", ErrSessionNotOwned, b.nodeName(sess.OwnerNode))
	}
	if err := ws.Sessions.SetModelRef(ctx, sessionID, ref); err != nil {
		return proto.SessionModel{}, err
	}
	return sessionModelView(cfg, ref), nil
}
