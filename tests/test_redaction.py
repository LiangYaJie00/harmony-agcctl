from agc_cli.redaction import redact_text, redact_value


def test_redacts_private_key_bearer_and_jwt() -> None:
    pem = "-----BEGIN PRIVATE KEY-----\nSECRET\n-----END PRIVATE KEY-----\n"
    token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
    text = f"header {pem} Authorization: Bearer {token} raw {token}"
    redacted = redact_text(text)
    assert "SECRET" not in redacted
    assert token not in redacted
    assert "Bearer [redacted]" in redacted


def test_redacts_secret_mapping_keys() -> None:
    cleaned = redact_value({"private_key": "abc", "taskId": "PUBLISH-1", "nested": [{"authorization": "Bearer abc"}]})
    assert cleaned["private_key"] == "[redacted]"
    assert cleaned["taskId"] == "PUBLISH-1"
    assert cleaned["nested"][0]["authorization"] == "[redacted]"
