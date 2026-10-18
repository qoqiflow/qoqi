# dbos-config: "enable_patching": True

@DBOS.workflow()
def expense(expense_id: str, employee: str, cents: int) -> str:
    notify_manager(expense_id)
    answer = DBOS.recv("answers", timeout_seconds=7 * 24 * 3600)

    if DBOS.patch("notify-accounting"):    # True for new workflows only
        notify_accounting(expense_id)
    # Later DBOS.deprecate_patch("notify-accounting"), then remove it
    # once the old workflows are done. Without a patch, a workflow is
    # recovered only by processes of its own application version.

    return pay(employee, cents)
