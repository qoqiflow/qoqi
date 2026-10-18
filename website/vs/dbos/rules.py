@DBOS.workflow()
def renewal(user: User) -> None:
    # Recovery calls this again: another hour, another path,
    # and DBOSStepNondeterminismError
    if datetime.now() < user.trial_ends:
        DBOS.sleep(2 * 24 * 3600)
    tip = random.choice(user.tips)          # another tip on recovery

    charge(user.id, tip)
