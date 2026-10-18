export async function renewal(user: User) {
  "use workflow";
  // Runs in a VM and is replayed: Date is a logical clock,
  // Math.random is seeded, and there is no fetch, no setTimeout,
  // no Node module. Everything real goes in a "use step" function.
  if (Date.now() < user.trialEnds) {
    await sleep("2d");
  }
  const tip = user.tips[Math.floor(Math.random() * user.tips.length)];
  await charge(user.id, tip);
}

async function charge(userId: string, tip: string) {
  "use step";
  // ...
}
