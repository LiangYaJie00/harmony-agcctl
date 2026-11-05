import json

from agc_cli.cli import run
from agc_cli.errors import ExitCode
from agc_cli.version import __version__
from tests.helpers import generate_private_pem, request_body, success_probe, write_config, write_private_json
from tests.test_doctor import FakeClient


def test_cli_prints_one_json_response(tmp_path, capsys) -> None:
    private_pem, _public = generate_private_pem()
    key_path = write_private_json(tmp_path, private_pem)
    config_path = write_config(tmp_path, key_path)
    request_path = tmp_path / "request.json"
    request_path.write_text(json.dumps(request_body()), encoding="utf-8")
    client = FakeClient(success_probe())

    code = run(
        ["doctor", "--config", str(config_path), "--request-file", str(request_path)],
        client=client,
    )

    stdout, stderr = capsys.readouterr()
    lines = [line for line in stdout.splitlines() if line.strip()]
    assert code == ExitCode.SUCCESS
    assert len(lines) == 1
    response = json.loads(lines[0])
    assert response["success"] is True
    assert response["operation"] == "DOCTOR"
    assert response["toolVersion"] == __version__
    assert private_pem not in stdout
    assert private_pem not in stderr
    assert "BEGIN PRIVATE KEY" not in stderr


def test_cli_rejects_invalid_request(tmp_path, capsys) -> None:
    private_pem, _public = generate_private_pem()
    key_path = write_private_json(tmp_path, private_pem)
    config_path = write_config(tmp_path, key_path)
    request_path = tmp_path / "request.json"
    payload = request_body()
    del payload["operationKey"]
    request_path.write_text(json.dumps(payload), encoding="utf-8")

    code = run(["doctor", "--config", str(config_path), "--request-file", str(request_path)])

    stdout, _stderr = capsys.readouterr()
    response = json.loads(stdout)
    assert code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert response["error"]["code"] == "SCHEMA_INVALID"
