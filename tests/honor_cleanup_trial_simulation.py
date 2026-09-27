"""Simulate only Lua orchestration; no shell, phone, credentials or actual deletion.
Run with lupa installed: python3 tests/honor_cleanup_trial_simulation.py
"""
from pathlib import Path
from lupa import LuaRuntime

SCRIPT = (Path(__file__).parents[1] / 'tests/honor_cleanup_trial.lua').read_text()
SETUP = r'''
bundle = 'com.levelinfinite.sgameGlobal'
data_path = '/private/var/mobile/Containers/Data/Application/1234-ABCD'
resource = 'Library/Application Support/' .. bundle .. '/QtsVFSCache'
recovery = '/var/mobile/Library/XXTCloudCleanupRecovery/honor-100'
resources = {[data_path .. '/' .. resource] = true}
reports, commands = {}, {}
files = {}
clear_calls, deny_writes, restore_attempts, close_calls = 0, 0, 0, 0
running = true
json = {encode=function(v) reports[#reports+1]=v;return v.phase end}
io = {
 open=function(path,mode)
  if (mode=='r' or mode=='rb') and not files[path] then return nil end
  return {write=function(_,value) files[path]=value;return true end,
          read=function() return files[path] end, close=function() return true end}
 end,
 popen=function(command)
  commands[#commands+1]=command
  return {read=function() return 'XXT_CLEAN_OK\n' end, close=function() return true end}
 end
}
os = {time=function() return 100 end, rename=function(source,destination)
 if source==recovery .. '/QtsVFSCache' then
  restore_attempts=restore_attempts+1
  if scenario=='restore_failure' then return false end
 end
 if not resources[source] then return false end
 resources[source]=nil;resources[destination]=true;return true
end}
plist = {write=function(path,value) current_policy=value;deny_writes=deny_writes+1 end,
 read=function() return current_policy end}
http = {post=function() return 200 end}
sys = {port=function() return 46952 end, msleep=function() end}
app = {
 close=function() close_calls=close_calls+1;running=scenario=='close_failure' end,
 is_running=function() return running end,
 data_path=function() return scenario=='bad_container' and '/private/var/mobile' or data_path end
}
clear = {app_data=function()
 clear_calls=clear_calls+1
 current_policy=nil
 if scenario=='clear_exception' then error('native cleanup exception') end
 return scenario~='clear_false'
end}
'''


def run(scenario):
    lua = LuaRuntime()
    lua.globals().scenario = scenario
    lua.execute(SETUP)
    lua.execute(SCRIPT)
    return lua.globals()


for scenario in ['success', 'clear_false', 'restore_failure', 'close_failure', 'bad_container', 'clear_exception']:
    g = run(scenario)
    report = g.reports[len(g.reports)]
    assert report.phase == ('awaiting_login_verification' if scenario == 'success' else 'failed'), scenario
    if scenario in ['close_failure', 'bad_container']:
        assert g.clear_calls == 0 and len(g.commands) == 0 and g.restore_attempts == 0, scenario
        assert g.resources[g.data_path + '/' + g.resource], scenario
        continue
    assert g.clear_calls == 1, scenario
    if scenario == 'restore_failure':
        assert g.restore_attempts == 1 and g.resources[g.recovery + '/QtsVFSCache'], scenario
        assert not g.resources[g.data_path + '/' + g.resource], scenario
        assert 'resource restore failed' in report.error, scenario
        continue
    assert g.restore_attempts == 1 and g.resources[g.data_path + '/' + g.resource], f'{scenario}: resource was not restored'
    assert g.deny_writes == 2 and g.current_policy.enabled and g.current_policy.allowedPriceQuotas['0.29'] == 0, f'{scenario}: missing final deny policy'
    print('PASS:', scenario)
print('PASS: failed close/container prevents cleanup; resource restore failure never reports success (simulated APIs only)')

lua = LuaRuntime()
lua.globals().scenario = 'success'
lua.execute(SETUP)
lua.execute(SCRIPT)
g = lua.globals()
before = (g.clear_calls, g.close_calls, g.deny_writes, len(g.commands), len(g.reports))
lua.execute(SCRIPT)
assert before == (g.clear_calls, g.close_calls, g.deny_writes, len(g.commands), len(g.reports)), 'replay changed the device or original report'
print('PASS: existing trial report blocks replay without overwriting recovery evidence')
