package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// What the daemon tells an answer that comes later.
var (
	// ErrLate is an attempt the daemon gave up, its deadline past or a
	// next attempt made: the answer is kept aside, not given to the flow,
	// and the step is to stop acting
	ErrLate = errors.New("the daemon gave the attempt up")
	// ErrToken is an answer whose token is not the attempt's
	ErrToken = errors.New("the token is not the attempt's")
	// ErrMisfit is a value that does not fit the step's type: the flow is
	// told the step failed
	ErrMisfit = errors.New("the answer does not fit the step")
)

// Daemon is where the daemon answers, for answers that come later: its
// address, as the application reaches it.
type Daemon struct {
	URL    string
	Client *http.Client
}

// Complete gives the answer of a step that said ErrLater, on a request of
// its own: once its work is done, before the task's deadline. It reports
// whether the daemon had an answer already, the first standing. Given the
// Keeper that keeps the step's answers, the answer is kept there first,
// under the claim the step left: sent again, the step gives it.
func (d Daemon) Complete(ctx context.Context, task *Task, answer Answer, keepers ...*Keeper) (already bool, err error) {
	for _, k := range keepers {
		if err := k.Answered(ctx, task, answer); err != nil {
			return false, err
		}
	}
	return d.post(ctx, task, answer)
}

// Working asks whether the attempt is still under way: ErrLate once the
// daemon gave it up, when a step that works for long stops acting.
func (d Daemon) Working(ctx context.Context, task *Task) error {
	_, err := d.post(ctx, task, Answer{Working: true})
	return err
}

func (d Daemon) post(ctx context.Context, task *Task, answer Answer) (bool, error) {
	fields := map[string]any{"key": task.Key, "attempt": task.Attempt, "token": task.Token}
	data, err := json.Marshal(answer)
	if err != nil {
		return false, err
	}
	var rest map[string]json.RawMessage
	json.Unmarshal(data, &rest)
	for k, v := range rest {
		fields[k] = v
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(d.URL, "/")+"/api/attempts", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	said, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		var got struct {
			Already bool `json:"already"`
		}
		json.Unmarshal(said, &got)
		return got.Already, nil
	case http.StatusGone:
		return false, ErrLate
	case http.StatusForbidden:
		return false, ErrToken
	case http.StatusUnprocessableEntity:
		return false, fmt.Errorf("%w: %s", ErrMisfit, strings.TrimSpace(string(said)))
	}
	return false, fmt.Errorf("the daemon answered %d: %s", resp.StatusCode, strings.TrimSpace(string(said)))
}
