package tools

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/question"
)

// QuestionToolName is the name of the question tool.
const QuestionToolName = "question"

//go:embed question.md
var questionDescription string

// QuestionParams are the parameters for the question tool.
type QuestionParams struct {
	// Kind selects how the question is rendered and how Options is
	// interpreted. One of: single_choice, multiple_choice, free_text,
	// yes_no.
	Kind string `json:"kind" description:"The question shape: \"single_choice\" (pick exactly one of Options), \"multiple_choice\" (pick zero or more of Options), \"free_text\" (open-ended text answer), or \"yes_no\" (a yes/no decision)."`
	// Prompt is the question text shown to the user.
	Prompt string `json:"prompt" description:"The question to ask, phrased so a short answer resolves it. State the blocking decision and, if helpful, the concrete options or tradeoffs."`
	// Options lists the choices for single_choice and
	// multiple_choice. Required (at least two entries) for those
	// kinds; ignored for free_text and yes_no.
	Options []string `json:"options,omitempty" description:"The choices to present. Required, with at least two entries, when kind is \"single_choice\" or \"multiple_choice\". Ignored otherwise."`
}

// QuestionResponseMetadata is attached to the tool response so the UI
// can render a compact summary of the answer in the transcript.
type QuestionResponseMetadata struct {
	Kind      string   `json:"kind"`
	Prompt    string   `json:"prompt"`
	Selected  []string `json:"selected,omitempty"`
	Cancelled bool     `json:"cancelled,omitempty"`
}

var validQuestionKinds = map[string]question.Kind{
	string(question.KindSingleChoice):   question.KindSingleChoice,
	string(question.KindMultipleChoice): question.KindMultipleChoice,
	string(question.KindFreeText):       question.KindFreeText,
	string(question.KindYesNo):          question.KindYesNo,
}

// QuestionCapable reports whether the question tool can currently
// reach an interactive user. It reuses the permission service's
// skip-requests flag (true in non-interactive/headless contexts such
// as `crush run` and YOLO mode) as the signal that no one is watching
// for prompts: in that state, permission requests are already
// auto-approved rather than shown, so a question would have no one to
// answer it either. Coordinators gate tool availability on this
// predicate and NewQuestionTool re-checks it at call time as a
// defense-in-depth hard-fail.
func QuestionCapable(permissions permission.Service) func(context.Context) bool {
	return func(context.Context) bool {
		return !permissions.SkipRequests()
	}
}

// NewQuestionTool returns the question tool. It asks the user a
// structured question and blocks until an answer arrives, but only
// when an interactive client is actually attached: if the workspace is
// running with permission prompts skipped (headless `crush run`, or
// YOLO mode), it hard-fails immediately with a clear error instead of
// blocking forever, so the agent can fall back to its own best
// assumption.
func NewQuestionTool(permissions permission.Service, questions question.Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		QuestionToolName,
		questionDescription,
		func(ctx context.Context, params QuestionParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			kind, ok := validQuestionKinds[params.Kind]
			if !ok {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"invalid kind %q: must be one of single_choice, multiple_choice, free_text, yes_no", params.Kind,
				)), nil
			}
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}
			if (kind == question.KindSingleChoice || kind == question.KindMultipleChoice) && len(params.Options) < 2 {
				return fantasy.NewTextErrorResponse(
					"options must have at least two entries for single_choice and multiple_choice questions",
				), nil
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.NewTextErrorResponse("session_id is required"), nil
			}

			if permissions.SkipRequests() {
				return fantasy.NewTextErrorResponse(
					"question tool unavailable: no interactive client is attached to answer (non-interactive/headless run, or permission prompts are being skipped). Do not retry — proceed by stating your assumption clearly and continuing.",
				), nil
			}

			// Known gap (deliberately not fixed here): SkipRequests() is a
			// proxy for "no interactive client". A server running with
			// skip=false but zero SSE clients subscribed still passes this
			// gate and blocks on Ask until the run's ctx is cancelled. This
			// mirrors the identical latent behavior of
			// permission.Service.Request today and is a separate follow-up;
			// there is no existing "answering client attached" signal to key
			// off in this architecture.
			answer, err := questions.Ask(ctx, question.CreateQuestionRequest{
				SessionID:  sessionID,
				ToolCallID: call.ID,
				Kind:       kind,
				Prompt:     params.Prompt,
				Options:    params.Options,
			})
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("question was not answered: %s", err)), nil
			}

			resp := fantasy.WithResponseMetadata(
				textResponseForAnswer(answer),
				QuestionResponseMetadata{
					Kind:      params.Kind,
					Prompt:    params.Prompt,
					Selected:  answer.Selected,
					Cancelled: answer.Cancelled,
				},
			)
			return resp, nil
		},
	)
}

// textResponseForAnswer renders the answer as the tool's text result.
func textResponseForAnswer(answer question.Answer) fantasy.ToolResponse {
	if answer.Cancelled {
		return fantasy.NewTextResponse(
			"The user declined to answer. Proceed using your best judgment and state the assumption you made.",
		)
	}
	if len(answer.Selected) == 0 {
		// The dialog blocks confirming an empty free-text / zero-
		// selection answer, but a client could still submit one over
		// the API, so make this actionable rather than an opaque
		// "(no answer)".
		return fantasy.NewTextResponse(
			"The user submitted no selection. Proceed using your best judgment and state the assumption you made.",
		)
	}
	return fantasy.NewTextResponse(fmt.Sprintf("%v", answer.Selected))
}
