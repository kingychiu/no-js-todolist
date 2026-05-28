package todolist

type TodoState string

const (
	Pending    TodoState = "pending"
	InProgress TodoState = "in_progress"
	Completed  TodoState = "completed"
)

func (s TodoState) CanTransitionTo(next TodoState) bool {
	switch s {
	case Pending:
		return next == InProgress
	case InProgress:
		return next == Completed
	}
	return false
}

func (s TodoState) Next() (TodoState, bool) {
	switch s {
	case Pending:
		return InProgress, true
	case InProgress:
		return Completed, true
	}
	return "", false
}
