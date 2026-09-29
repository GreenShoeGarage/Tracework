"""Load with pytest -p tracework_pytest. GPL-3.0-only."""
import pytest
from tracework import Client
from tracework_checks import Checks

def pytest_addoption(parser):
 group=parser.getgroup('tracework');group.addoption('--tracework-url',default=None,help='Local TRACEWORK URL; default TRACEWORK_URL');group.addoption('--tracework-project',default=None);group.addoption('--tracework-session',default=None)

@pytest.fixture
def tracework_checks(request):
 """Returns the native Checks client; missing evidence fails, never silently skips."""
 return Checks(Client(url=request.config.getoption('--tracework-url')))

@pytest.fixture
def tracework_recording(request):
 project=request.config.getoption('--tracework-project');session=request.config.getoption('--tracework-session')
 if not project or not session:raise pytest.UsageError('--tracework-project and --tracework-session are required')
 return project,session
