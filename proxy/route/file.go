package route

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadRuleFile 支持 Loyalsoldier/clash-rules 的 payload: YAML 列表，
// 也支持自定义逐行域名列表。仅在创建路由 Manager 时调用。
func loadRuleFile(path string, add func(string) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open rule file %q: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	yaml := false
	count := 0
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "payload:" {
			if yaml || count > 0 {
				return fmt.Errorf("rule file %q line %d: unexpected payload header", path, lineNumber)
			}
			yaml = true
			continue
		}
		if yaml {
			if !strings.HasPrefix(line, "- ") {
				return fmt.Errorf("rule file %q line %d: expected YAML list item", path, lineNumber)
			}
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		}
		// 规则集使用单引号，兼容未加引号和双引号的条目。
		if len(line) >= 2 && ((line[0] == '\'' && line[len(line)-1] == '\'') || (line[0] == '"' && line[len(line)-1] == '"')) {
			line = line[1 : len(line)-1]
		}
		if line == "" {
			return fmt.Errorf("rule file %q line %d: empty rule", path, lineNumber)
		}
		if err := add(line); err != nil {
			return fmt.Errorf("rule file %q line %d: %w", path, lineNumber, err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read rule file %q: %w", path, err)
	}
	if count == 0 {
		return fmt.Errorf("rule file %q contains no rules", path)
	}
	return nil
}
