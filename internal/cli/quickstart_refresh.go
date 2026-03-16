package cli

import (
	"fmt"

	"github.com/bmf/links-issue-tracker/internal/workspace"
)

type quickstartRefreshItem struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type quickstartRefreshReport struct {
	Agents quickstartRefreshItem `json:"agents"`
	Hooks  quickstartRefreshItem `json:"hooks"`
}

func refreshQuickstartManagedAssets(ws workspace.Info) (quickstartRefreshReport, error) {
	// [LAW:single-enforcer] Quickstart refresh reuses the existing managed writers so AGENTS and hook updates stay owned at one boundary.
	hookResult, hookErr := installHooks(ws)
	if hookErr != nil {
		return quickstartRefreshReport{}, hookErr
	}
	agentsResult, agentsErr := rewriteLinksAgentsFile(ws.RootDir)
	if agentsErr != nil {
		return quickstartRefreshReport{}, agentsErr
	}
	return quickstartRefreshReport{
		Hooks: quickstartRefreshItem{
			Path:   hookResult.HookPath,
			Status: managedAssetStatus(hookResult.Changed, false),
		},
		Agents: quickstartRefreshItem{
			Path:   agentsResult.Path,
			Status: managedAssetStatus(agentsResult.Changed, agentsResult.Created),
		},
	}, nil
}

func managedAssetStatus(changed bool, created bool) string {
	statuses := []string{"unchanged", "updated", "created"}
	index := 0
	if changed {
		index = 1
	}
	if created {
		index = 2
	}
	return statuses[index]
}

func formatQuickstartRefreshSummary(refresh quickstartRefreshReport) string {
	return fmt.Sprintf("hooks=%s agents=%s", refresh.Hooks.Status, refresh.Agents.Status)
}
