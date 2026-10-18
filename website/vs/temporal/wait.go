var answer Answer
answered := false
timerCtx, cancelTimer := workflow.WithCancel(ctx)
sel := workflow.NewSelector(ctx)
sel.AddReceive(workflow.GetSignalChannel(ctx, "answers"),
	func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &answer)
		answered = true
		cancelTimer()
	})
sel.AddFuture(workflow.NewTimer(timerCtx, 7*24*time.Hour),
	func(f workflow.Future) {})
sel.Select(ctx)
if !answered {
	return "expired", nil
}
