from agc_cli.ret_codes import ret_failure


def test_follow_msg_appends_huawei_message() -> None:
    message, detail = ret_failure(204144641, "versionId 为空")
    assert message == "无效入参：versionId 为空"
    assert detail == "204144641: versionId 为空"


def test_unknown_error_adds_ticket_hint() -> None:
    message, detail = ret_failure(204144642, "unknown error")
    assert message == "系统错误：unknown error。可通过在线工单联系华为"
    assert detail == "204144642: unknown error"


def test_missing_message_uses_code() -> None:
    message, detail = ret_failure(42, None)
    assert message == "华为接口返回业务错误（42）"
    assert detail == "42: "
