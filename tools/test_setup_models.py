import tempfile
from pathlib import Path
import pytest
from setup_models import pricing, source_env, write_private


def test_budget_is_explicit_and_prices_keep_their_actual_units():
    for bad in ('', '0', '-1', '1e3', '1.0000001', ' 1'):
        with pytest.raises(ValueError): pricing(bad)
    chosen = pricing('1')
    assert chosen['monthly_budget_cny'] == '1'
    assert chosen['asr_billing_mode'] == 'seconds'
    assert chosen['asr_cny_per_second'] == '0.00022'
    assert chosen['asr_input_cny_per_million_tokens'] == ''


def test_key_source_rejects_interpolation_and_line_injection():
    for key in ('', 'sk-${HOME}-secret', 'sk-one\nOTHER=secret', 'sk-`secret`', 'sk-$(secret)'):
        with pytest.raises(ValueError): source_env(key)
    assert 'synthetic-key-12345' in source_env('synthetic-key-12345')


def test_private_files_do_not_overwrite_existing_configuration():
    with tempfile.TemporaryDirectory() as folder:
        path = Path(folder)/'config.private.json'
        write_private(path, 'synthetic')
        with pytest.raises(FileExistsError): write_private(path, 'replacement')
        assert path.read_text() == 'synthetic'
