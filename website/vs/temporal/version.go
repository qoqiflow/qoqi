func Expense(ctx workflow.Context, exp Expense) (string, error) {
	// ... notify, wait for the answer ...

	v := workflow.GetVersion(ctx, "notify-accounting", workflow.DefaultVersion, 1)
	if v != workflow.DefaultVersion {
		err := workflow.ExecuteActivity(ctx, NotifyAccounting, exp.ID).Get(ctx, nil)
		if err != nil {
			return "", err
		}
	}
	// Keep this branch until every execution started before it
	// has left retention. One GetVersion per change, for good.

	var payout string
	err := workflow.ExecuteActivity(ctx, Pay, exp).Get(ctx, &payout)
	return payout, err
}
