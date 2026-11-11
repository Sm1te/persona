package prompts

// RenderQuestionCancelled reports a closed question without inventing an answer.
func RenderQuestionCancelled() string { return render("agents/question_cancelled", nil) }

// RenderQuestionUserSpoke tells ask_user's caller why a waiting question was withdrawn: the user wrote in the conversation.
func RenderQuestionUserSpoke() string { return render("agents/question_user_spoke", nil) }

// QuestionMovedToTodo is what ask_user returns when nobody answered in time and the question became a to-do (#1617).
type QuestionMovedToTodo struct {
	TodoID string
	Title  string
	// DefaultAt: when its recommended options are taken by default (a reversible question whose deadline is after its
	// card's wait, #3127), MM-DD HH:MM; Overdue: its deadline passed, but it cannot be undone, so nothing was taken.
	DefaultAt string
	Overdue   bool
}

func RenderQuestionMovedToTodo(q QuestionMovedToTodo) string {
	return render("agents/question_moved_to_todo", q)
}

// QuestionDefaulted is what ask_user returns when nobody answered by its deadline and the question could be undone: its
// recommended options were taken by default and recorded on to-do TodoID (#3127).
type QuestionDefaulted struct {
	Deadline string // MM-DD HH:MM in the user's zone
	TodoID   string
	Choices  []AskTodoChoice
}

func RenderQuestionDefaulted(q QuestionDefaulted) string {
	return render("agents/question_defaulted", q)
}

// QuestionAskedLater is what ask_user returns with wait false (#4928): the card is up (interaction ID) until Until
// (MM-DD HH:MM in the user's zone), and how it ends comes later as a new message.
type QuestionAskedLater struct {
	ID    string
	Until string
}

func RenderQuestionAskedLater(q QuestionAskedLater) string {
	return render("agents/question_asked_later", q)
}

// RenderSettingUnanswered is what persona_settings returns when its approval card ended without the user's own allow
// or deny: it expired, was closed, or someone else answered it (#3722).
func RenderSettingUnanswered() string { return render("agents/setting_unanswered", nil) }
