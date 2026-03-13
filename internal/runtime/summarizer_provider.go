package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/SemSwitch/Nexis-Echo/internal/config"
	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

type heuristicSummaryProvider struct {
	maxLines int
}

func providerFromConfig(cfg config.SummarizerConfig) SummaryProvider {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "heuristic":
		return heuristicSummaryProvider{maxLines: cfg.MaxBatchSize}
	default:
		return heuristicSummaryProvider{maxLines: cfg.MaxBatchSize}
	}
}

func (p heuristicSummaryProvider) Name() string {
	return "heuristic"
}

func (p heuristicSummaryProvider) Summarize(_ context.Context, block model.OutputBlock) (SummaryResult, error) {
	lines := summarizeLines(block.Content, p.maxLines)
	severity := detectSeverity(block.Status, lines)
	excerpt := selectExcerpt(severity, lines)

	summary := buildSummary(block.Source, severity)
	if excerpt == "" {
		excerpt = fallbackExcerpt(block.Status, lines)
	}

	return SummaryResult{
		Summary:  summary,
		Excerpt:  excerpt,
		Severity: severity,
	}, nil
}

func summarizeLines(content string, maxLines int) []string {
	rawLines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}

	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return lines
}

func detectSeverity(status string, lines []string) model.Severity {
	status = strings.ToLower(strings.TrimSpace(status))
	text := strings.ToLower(strings.Join(lines, "\n"))

	if containsAny(status, "error", "fail", "failed", "fatal") || containsAny(text, "error", "failed", "panic", "traceback", "exception", "fatal") {
		return model.SeverityError
	}
	if containsAny(status, "warn", "warning") || containsAny(text, "warning", "warn", "deprecated") {
		return model.SeverityWarning
	}
	if containsAny(status, "success", "ok", "passed", "complete", "completed", "done") || containsAny(text, "completed successfully", "build succeeded", "tests passed") {
		return model.SeveritySuccess
	}

	return model.SeverityInfo
}

func selectExcerpt(severity model.Severity, lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	switch severity {
	case model.SeverityError:
		for i := len(lines) - 1; i >= 0; i-- {
			if containsAny(strings.ToLower(lines[i]), "error", "failed", "panic", "traceback", "exception", "fatal") {
				return trimExcerpt(lines[i])
			}
		}
	case model.SeverityWarning:
		for i := len(lines) - 1; i >= 0; i-- {
			if containsAny(strings.ToLower(lines[i]), "warning", "warn", "deprecated") {
				return trimExcerpt(lines[i])
			}
		}
	}

	return trimExcerpt(lines[len(lines)-1])
}

func fallbackExcerpt(status string, lines []string) string {
	status = strings.TrimSpace(status)
	if status != "" {
		return trimExcerpt(status)
	}
	if len(lines) == 0 {
		return ""
	}

	return trimExcerpt(lines[len(lines)-1])
}

func buildSummary(source string, severity model.Severity) string {
	label := humanizeSource(source)

	switch severity {
	case model.SeverityError:
		return fmt.Sprintf("%s failed.", label)
	case model.SeverityWarning:
		return fmt.Sprintf("%s completed with warnings.", label)
	case model.SeveritySuccess:
		return fmt.Sprintf("%s completed successfully.", label)
	default:
		return fmt.Sprintf("%s finished.", label)
	}
}

func humanizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "Task"
	}

	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(source))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	if len(parts) == 0 {
		return "Task"
	}

	return strings.Join(parts, " ")
}

func trimExcerpt(value string) string {
	const maxExcerptLen = 180

	value = strings.TrimSpace(value)
	if len(value) <= maxExcerptLen {
		return value
	}

	return strings.TrimSpace(value[:maxExcerptLen-3]) + "..."
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}

	return false
}
