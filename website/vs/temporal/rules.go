func Renewal(ctx workflow.Context, user User) error {
	// Replayed from its history: no time.Now(), it's workflow.Now(ctx)
	if workflow.Now(ctx).Before(user.TrialEnds) {
		// no time.Sleep: workflow.Sleep
		_ = workflow.Sleep(ctx, 48*time.Hour)
	}

	// no rand: wrap it in a SideEffect, or it changes on replay
	var pick int
	_ = workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return rand.Intn(len(user.Tips))
	}).Get(&pick)

	// no goroutines, channels, select, or ranging over a map
	return workflow.ExecuteActivity(ctx, Charge, user.ID, user.Tips[pick]).Get(ctx, nil)
}
