// Version 1
await step.run("charge", () => charge(order));

// Version 2, deployed while runs are in flight: a cleaner name.
// A step is known by its id, so every run already past
// "charge" meets "charge-card" for the first time, and pays again.
await step.run("charge-card", () => charge(order));
