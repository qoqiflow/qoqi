@DBOS.workflow()
def renewal(user: User) -> None:
    # Not a step: runs again on every recovery.
    # Nothing records it, nothing warns.
    requests.post("https://hooks.example.com/renewals", data=user.id)
    charge(user.id)
