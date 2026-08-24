package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/services/execute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type planConfig struct {
	Slim struct {
		RequireScope bool
	}
	RootResolve struct {
		Batch bool
	}
	Bitrate struct {
		BatchUpdate bool
	}
}

func defaultPlanConfig() planConfig {
	defaults := appconfig.DefaultAppConfig()
	out := planConfig{}
	out.Slim.RequireScope = defaults.Plan.Slim.RequireScope
	out.RootResolve.Batch = defaults.Plan.RootResolve.Batch
	out.Bitrate.BatchUpdate = defaults.Plan.Bitrate.BatchUpdate
	return out
}

// getPruneRegexPattern reads the prune.regex_pattern from config.json.
func (s *OnseiServer) getPruneRegexPattern() (string, error) {
	cfgPath := filepath.Join(s.configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", fmt.Errorf("read config file: %w", err)
	}

	var cfg appconfig.AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse config JSON: %w", err)
	}

	if cfg.Prune.RegexPattern == "" {
		return "", fmt.Errorf("prune regex_pattern is empty in config")
	}

	return cfg.Prune.RegexPattern, nil
}

// getToolsConfig reads the tools config from config.json.
func (s *OnseiServer) getToolsConfig() (execute.ToolsConfig, error) {
	cfgPath := filepath.Join(s.configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		// Return empty config if file doesn't exist.
		if os.IsNotExist(err) {
			return execute.ToolsConfig{}, nil
		}
		return execute.ToolsConfig{}, fmt.Errorf("read config file: %w", err)
	}

	cfg := appconfig.DefaultAppConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return execute.ToolsConfig{}, fmt.Errorf("parse config JSON: %w", err)
	}

	return execute.ToolsConfig{
		Encoder:  cfg.Tools.Encoder,
		QAACPath: cfg.Tools.QAACPath,
		LAMEPath: cfg.Tools.LAMEPath,
	}, nil
}

// getExecuteConfig reads the execute config from config.json.
func (s *OnseiServer) getExecuteConfig() (execute.ExecuteConfig, error) {
	cfgPath := filepath.Join(s.configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			defaults := appconfig.DefaultAppConfig()
			return execute.ExecuteConfig{MaxIOWorkers: defaults.Execute.MaxIOWorkers, PrecheckConcurrentStat: defaults.Execute.Precheck.ConcurrentStat}, nil
		}
		defaults := appconfig.DefaultAppConfig()
		return execute.ExecuteConfig{MaxIOWorkers: defaults.Execute.MaxIOWorkers, PrecheckConcurrentStat: defaults.Execute.Precheck.ConcurrentStat}, fmt.Errorf("read config file: %w", err)
	}

	cfg := appconfig.DefaultAppConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		defaults := appconfig.DefaultAppConfig()
		return execute.ExecuteConfig{MaxIOWorkers: defaults.Execute.MaxIOWorkers, PrecheckConcurrentStat: defaults.Execute.Precheck.ConcurrentStat}, fmt.Errorf("parse config JSON: %w", err)
	}

	workers := cfg.Execute.MaxIOWorkers
	if workers < 1 {
		workers = 4 // Default value
	}

	return execute.ExecuteConfig{MaxIOWorkers: workers, PrecheckConcurrentStat: cfg.Execute.Precheck.ConcurrentStat}, nil
}

func (s *OnseiServer) getPlanConfig() (planConfig, error) {
	out := defaultPlanConfig()

	cfgPath := filepath.Join(s.configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("read config file: %w", err)
	}

	cfg := appconfig.DefaultAppConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return out, fmt.Errorf("parse config JSON: %w", err)
	}

	out.Slim.RequireScope = cfg.Plan.Slim.RequireScope
	out.RootResolve.Batch = cfg.Plan.RootResolve.Batch
	out.Bitrate.BatchUpdate = cfg.Plan.Bitrate.BatchUpdate
	return out, nil
}

// GetConfig returns the current configuration as JSON.
func (s *OnseiServer) GetConfig(_ context.Context, _ *pb.GetConfigRequest) (*pb.GetConfigResponse, error) {
	cfgPath := filepath.Join(s.configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &pb.GetConfigResponse{ConfigJson: "{}"}, nil
		}
		return nil, status.Errorf(codes.Internal, "read config: %v", err)
	}
	return &pb.GetConfigResponse{ConfigJson: string(data)}, nil
}

// UpdateConfig writes configuration JSON to disk.
func (s *OnseiServer) UpdateConfig(_ context.Context, req *pb.UpdateConfigRequest) (*pb.UpdateConfigResponse, error) {
	cfgPath := filepath.Join(s.configDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(req.ConfigJson), 0644); err != nil {
		return nil, status.Errorf(codes.Internal, "write config: %v", err)
	}
	return &pb.UpdateConfigResponse{}, nil
}
