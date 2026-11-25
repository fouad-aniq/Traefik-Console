package validation

import (
	"fmt"
	"strings"
)

// detectMiddlewareCycles uses Tarjan/DFS to find cycles in chain definitions.
func (cv *ConfigValidator) detectMiddlewareCycles(chains map[string][]string) []Issue {
	var issues []Issue
	visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited

	var dfs func(node string, path []string)
	dfs = func(node string, path []string) {
		visited[node] = 1
		currentPath := append(path, node)

		for _, neighbor := range chains[node] {
			cleanNeighbor := strings.TrimSuffix(neighbor, "@file")
			if visited[cleanNeighbor] == 1 {
				cycleStr := strings.Join(append(currentPath, cleanNeighbor), " -> ")
				issues = append(issues, Issue{
					Path:     fmt.Sprintf("http.middlewares.%s.chain", node),
					Message:  fmt.Sprintf("Cyclic middleware chain dependency detected: %s", cycleStr),
					RuleCode: "MIDDLEWARE_CYCLE_DETECTED",
				})
			} else if visited[cleanNeighbor] == 0 {
				if _, isChain := chains[cleanNeighbor]; isChain {
					dfs(cleanNeighbor, currentPath)
				}
			}
		}

		visited[node] = 2
	}

	for chainName := range chains {
		if visited[chainName] == 0 {
			dfs(chainName, nil)
		}
	}

	return issues
}
