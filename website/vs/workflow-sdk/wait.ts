using hook = createHook<Answer>({ token: `expense:${id}` });

const answer = await Promise.race([
  hook,
  sleep("7d").then(() => "expired" as const),
]);
if (answer === "expired") return { status: "expired" };

// and a route of your app, which must check the caller itself:
// the token routes the answer, it does not authorize it
await resumeHook(`expense:${id}`, { approved: true, by: "grace" });
