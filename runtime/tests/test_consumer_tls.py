"""Tests for _build_nats_tls_context in consumer.py."""
from __future__ import annotations

import logging
import shutil
import ssl
import subprocess

import pytest

from kape_runtime.consumer import _build_nats_tls_context

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_OPENSSL = shutil.which("openssl")


def _generate_test_certs(tmp_path):
    """Generate a self-signed CA + client cert/key in tmp_path using openssl.

    Returns (ca_file, cert_file, key_file) as str paths, or raises
    pytest.skip if openssl is unavailable.
    """
    if _OPENSSL is None:
        pytest.skip("openssl not found on PATH — skipping cert-gen test")

    ca_key = str(tmp_path / "ca.key")
    ca_crt = str(tmp_path / "ca.crt")
    tls_key = str(tmp_path / "tls.key")
    tls_csr = str(tmp_path / "tls.csr")
    tls_crt = str(tmp_path / "tls.crt")

    # Generate CA key + self-signed cert
    subprocess.check_call(
        [_OPENSSL, "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", ca_key],
        stderr=subprocess.DEVNULL,
    )
    subprocess.check_call(
        [
            _OPENSSL, "req", "-new", "-x509",
            "-key", ca_key,
            "-out", ca_crt,
            "-days", "1",
            "-subj", "/CN=kape-test-ca",
        ],
        stderr=subprocess.DEVNULL,
    )

    # Generate client key + CSR
    subprocess.check_call(
        [_OPENSSL, "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", tls_key],
        stderr=subprocess.DEVNULL,
    )
    subprocess.check_call(
        [
            _OPENSSL, "req", "-new",
            "-key", tls_key,
            "-out", tls_csr,
            "-subj", "/CN=kape-handler",
        ],
        stderr=subprocess.DEVNULL,
    )

    # Sign client cert with CA
    subprocess.check_call(
        [
            _OPENSSL, "x509", "-req",
            "-in", tls_csr,
            "-CA", ca_crt,
            "-CAkey", ca_key,
            "-CAcreateserial",
            "-out", tls_crt,
            "-days", "1",
        ],
        stderr=subprocess.DEVNULL,
    )

    return ca_crt, tls_crt, tls_key


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


def test_returns_none_when_env_unset(monkeypatch, caplog):
    """Returns None and logs a warning when all three env vars are absent."""
    monkeypatch.delenv("NATS_TLS_CERT", raising=False)
    monkeypatch.delenv("NATS_TLS_KEY", raising=False)
    monkeypatch.delenv("NATS_TLS_CA", raising=False)

    with caplog.at_level(logging.WARNING, logger="kape_runtime.consumer"):
        result = _build_nats_tls_context()

    assert result is None
    assert any("without mTLS" in record.message for record in caplog.records)


def test_returns_none_when_one_env_missing(monkeypatch, caplog, tmp_path):
    """Returns None when only two of the three env vars are set."""
    monkeypatch.setenv("NATS_TLS_CERT", str(tmp_path / "tls.crt"))
    monkeypatch.setenv("NATS_TLS_KEY", str(tmp_path / "tls.key"))
    monkeypatch.delenv("NATS_TLS_CA", raising=False)

    with caplog.at_level(logging.WARNING, logger="kape_runtime.consumer"):
        result = _build_nats_tls_context()

    assert result is None


def test_returns_context_when_all_env_set(monkeypatch, tmp_path):
    """Returns a valid SSLContext with TLS 1.3 minimum when all env vars are set."""
    ca_crt, tls_crt, tls_key = _generate_test_certs(tmp_path)

    monkeypatch.setenv("NATS_TLS_CERT", tls_crt)
    monkeypatch.setenv("NATS_TLS_KEY", tls_key)
    monkeypatch.setenv("NATS_TLS_CA", ca_crt)

    result = _build_nats_tls_context()

    assert isinstance(result, ssl.SSLContext)
    assert result.minimum_version == ssl.TLSVersion.TLSv1_3
