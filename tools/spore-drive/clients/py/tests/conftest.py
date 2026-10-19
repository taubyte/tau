import pytest

from tests.mock import start_mock, stop_mock


@pytest.fixture(scope="session")
def mock_url():
    proc, url = start_mock()
    yield url
    stop_mock(proc)
