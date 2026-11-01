package bench

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// LogTask is one request as llama.cpp's own log accounts for it. The log
// states three counts per task: the prompt tokens it evaluated (the
// prompt_n of the response, the prompt less what its cache held), the tokens
// it generated, and the number of tokens the slot held when it was released.
type LogTask struct {
	// Task is llama.cpp's task number and Slot the slot it ran on.
	Task, Slot int
	// Prefilled is the prompt tokens evaluated ("prompt eval time = ... /
	// N tokens").
	Prefilled int
	// Completion is the tokens generated ("eval time = ... / N tokens").
	Completion int
	// Held is the slot's token count at release ("stop processing: n_tokens
	// = N"): the prompt and the generated tokens, less the last token, which
	// is sampled but not yet part of the cache.
	Held int
	// Truncated is whether the server cut the prompt to fit its context.
	Truncated bool
}

var (
	logPromptEval = regexp.MustCompile(`slot print_timing: id\s+(\d+) \| task (\d+) \| prompt eval time =\s*[\d.]+ ms /\s*(\d+) tokens`)
	logEval       = regexp.MustCompile(`slot print_timing: id\s+(\d+) \| task (\d+) \|\s+eval time =\s*[\d.]+ ms /\s*(\d+) tokens`)
	logRelease    = regexp.MustCompile(`slot\s+release: id\s+(\d+) \| task (\d+) \| stop processing: n_tokens = (\d+), truncated = (\d)`)
)

// ParseServerLog reads the finished tasks out of a llama.cpp log, in the order
// the server released them. A task whose release is not in the log (cut off at
// the start of the window, or still running) is left out, since only a
// complete set of counts says anything. It reads the format of the build the
// bench runs (b11942); a build that words these lines differently yields no
// task, and the caller fails on that rather than passing.
func ParseServerLog(log string) []LogTask {
	type partial struct {
		slot            int
		prefilled, gen  int
		hasPre, hasEval bool
	}
	open := map[int]*partial{}
	get := func(task, slot int) *partial {
		p, ok := open[task]
		if !ok {
			p = &partial{slot: slot}
			open[task] = p
		}
		return p
	}
	var tasks []LogTask
	for _, line := range strings.Split(log, "\n") {
		switch {
		case strings.Contains(line, "prompt eval time"):
			if m := logPromptEval.FindStringSubmatch(line); m != nil {
				p := get(atoi(m[2]), atoi(m[1]))
				p.prefilled, p.hasPre = atoi(m[3]), true
			}
		case strings.Contains(line, "eval time ="):
			if m := logEval.FindStringSubmatch(line); m != nil {
				p := get(atoi(m[2]), atoi(m[1]))
				p.gen, p.hasEval = atoi(m[3]), true
			}
		case strings.Contains(line, "stop processing"):
			m := logRelease.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			task := atoi(m[2])
			p, ok := open[task]
			if !ok || !p.hasPre || !p.hasEval {
				continue
			}
			delete(open, task)
			tasks = append(tasks, LogTask{
				Task: task, Slot: atoi(m[1]), Prefilled: p.prefilled, Completion: p.gen,
				Held: atoi(m[3]), Truncated: m[4] == "1",
			})
		}
	}
	return tasks
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// String states the task for a message.
func (t LogTask) String() string {
	return fmt.Sprintf("task %d on slot %d: prefilled %d, generated %d, held %d", t.Task, t.Slot, t.Prefilled, t.Completion, t.Held)
}
