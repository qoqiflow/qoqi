run: async (ctx: restate.WorkflowContext, expense: Expense) => {
  let answer: Answer;
  try {
    answer = await ctx.promise<Answer>("answer").get().orTimeout({ days: 7 });
  } catch (e) {
    if (e instanceof restate.TimeoutError) return { status: "expired" };
    throw e;
  }
  // ...
},

// and a handler of its own to deliver the answer
answer: async (ctx: restate.WorkflowSharedContext, answer: Answer) => {
  await ctx.promise<Answer>("answer").resolve(answer);
},
