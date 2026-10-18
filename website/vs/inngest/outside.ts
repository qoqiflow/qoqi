inngest.createFunction(
  { id: "trial", triggers: { event: "trial/started" } },
  async ({ event, step }) => {
    // Outside a step: runs again each time Inngest calls the function back
    const user = await db.users.find(event.data.userId);
    await analytics.track("trial_started", user.id);   // tracked again, and again

    await step.sleep("wait-3-days", "3d");
    await step.run("send-tips", () => sendTips(user.email));
  },
);
