run: async (ctx: restate.WorkflowContext, user: User) => {
  // Replayed from its journal: not Date.now(), ctx.date.now()
  if ((await ctx.date.now()) < user.trialEnds) {
    await ctx.sleep({ days: 2 });
  }
  // not Math.random(): ctx.rand
  const tip = user.tips[Math.floor(ctx.rand.random() * user.tips.length)];

  // Forgot ctx.run? It runs again on every replay, unrecorded
  await fetch("https://hooks.example.com/renewals", { method: "POST", body: user.id });
  await ctx.run("charge", () => charge(user.id, tip));
},
