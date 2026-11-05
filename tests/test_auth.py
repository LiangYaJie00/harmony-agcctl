import json
import time

import jwt

from agc_cli.auth import TOKEN_AUDIENCE, TOKEN_LIFETIME_SECONDS, build_jwt, load_service_account
from tests.helpers import KEY_ID, SUB_ACCOUNT, generate_private_pem, write_private_json


def test_jwt_uses_ps256_and_is_not_written(tmp_path) -> None:
    private_pem, public_pem = generate_private_pem()
    key_path = write_private_json(tmp_path, private_pem)
    before = sorted(path.name for path in tmp_path.iterdir())
    account = load_service_account(key_path)
    issued_at = int(time.time())

    token = build_jwt(account, issued_at=issued_at)

    after = sorted(path.name for path in tmp_path.iterdir())
    header = jwt.get_unverified_header(token)
    claims = jwt.decode(token, public_pem, algorithms=["PS256"], audience=TOKEN_AUDIENCE)
    assert header == {"alg": "PS256", "kid": KEY_ID, "typ": "JWT"}
    assert claims["iss"] == SUB_ACCOUNT
    assert claims["iat"] == issued_at
    assert claims["exp"] == issued_at + TOKEN_LIFETIME_SECONDS
    assert after == before
    assert "private_key" not in repr(account)
    stored = json.loads(key_path.read_text(encoding="utf-8"))
    assert stored["private_key"] == private_pem


def test_rejects_private_json_without_key(tmp_path) -> None:
    path = tmp_path / "private.json"
    path.write_text(json.dumps({"key_id": "kid", "sub_account": "sub"}), encoding="utf-8")
    path.chmod(0o600)
    try:
        load_service_account(path)
    except ValueError as exc:
        assert "private_key" in str(exc)
    else:
        raise AssertionError("expected ValueError")
