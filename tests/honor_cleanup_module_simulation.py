"""Exercise the production cleanup module with in-memory XXTouch/filesystem APIs only."""
from pathlib import Path
from lupa import LuaRuntime

SCRIPT = (Path(__file__).parents[1] / 'server/order_assets/honor_cleanup.lua').read_text()
SETUP = r'''
bundle='com.levelinfinite.sgameGlobal'
root='/private/var/mobile/Containers/Data/Application/1234-ABCD'
work='/var/mobile/Library/XXTCloudCleanupRecovery/honor-production'
resource='Library/Application Support/'..bundle..'/QtsVFSCache'
nodes, content, objects = {}, {}, {}
clear_calls, deny_writes = 0,0
function mkdir(p)
 local path=''
 for part in p:gmatch('[^/]+') do path=path..'/'..part;nodes[path]='directory' end
end
mkdir(root..'/'..resource)
mkdir(root..'/Library/Preferences')
nodes[root..'/Library/Preferences/user.plist']='file'
nodes[root..'/'..resource..'/asset']='file'
json={encode=function(v) objects[#objects+1]=v;return tostring(#objects) end,
 decode=function(s) return objects[tonumber(s)] end}
local fs={symlinkattributes=function(p,field) return nodes[p] end,
 dir=function(p)
  assert(nodes[p]=='directory','missing directory '..p)
  local entries={};local prefix=p..'/'
  for name in pairs(nodes) do
   if name:sub(1,#prefix)==prefix then
    local suffix=name:sub(#prefix+1)
    if not suffix:find('/') then entries[#entries+1]=suffix end
   end
  end
  local index=0;return function() index=index+1;return entries[index] end
 end}
require=function(name) assert(name=='lfs');return fs end
io={open=function(p,mode)
 if mode=='r' and not content[p] then return nil end
 return {read=function() return content[p] end,close=function() return true end,
 write=function(_,s) nodes[p]='file';content[p]=s;return true end}
 end,
 popen=function(command)
  if command:find('mkdir %-p') then
   for path in command:gmatch("'([^']+)'") do mkdir((path:gsub('^/rootfs',''))) end
  end
  return {read=function() return 'XXT_CLEAN_OK\n' end,close=function() return true end}
 end}
os={rename=function(a,b)
 if scenario=='restore_failure' and a==work..'/QtsVFSCache' then return false end
 if not nodes[a] then return false end
 local moving={}
 for p,kind in pairs(nodes) do
  if p==a or p:sub(1,#a+1)==a..'/' then moving[p]=kind end
 end
 for p,kind in pairs(moving) do
  local target=b..p:sub(#a+1);nodes[target]=kind;nodes[p]=nil
  content[target]=content[p];content[p]=nil
 end
 return true
end}
app={is_running=function() return scenario=='running' or (scenario=='reopened' and clear_calls>0) end,
 data_path=function() return scenario=='bad_container' and '/private/var/mobile' or root end}
plist={write=function(p,v) deny_writes=deny_writes+1;nodes[p]='file';policy=v end,
 read=function() return policy end}
clear={app_data=function()
 clear_calls=clear_calls+1
 local remove={}
 for p in pairs(nodes) do if p:sub(1,#root+1)==root..'/' then remove[#remove+1]=p end end
 for _,p in ipairs(remove) do nodes[p]=nil;content[p]=nil end
 mkdir(root..'/Documents');mkdir(root..'/Library');mkdir(root..'/tmp')
 if scenario=='residue' then nodes[root..'/Documents/account']='file' end
 if scenario=='clear_exception' then error('native clear error') end
 return scenario~='clear_false'
end}
'''

for scenario in ['success', 'clear_false', 'clear_exception', 'restore_failure', 'running', 'bad_container', 'residue', 'old_stage', 'old_failure', 'reopened']:
    lua = LuaRuntime(unpack_returned_tuples=True)
    lua.globals().scenario = scenario
    lua.execute(SETUP)
    g = lua.globals()
    if scenario == 'old_stage':
        lua.execute("mkdir(work..'/QtsVFSCache')")
    if scenario == 'old_failure':
        lua.execute("mkdir(work);content[work..'/state.json']=json.encode({phase='failed',container=root})")
    module = lua.execute(SCRIPT)
    assert not module.verify(g.bundle), scenario
    result = module.clean(g.bundle)
    ok = result[0] if isinstance(result, tuple) else result
    assert bool(ok) == (scenario == 'success'), (scenario, result)
    assert bool(module.verify(g.bundle)) == (scenario == 'success'), scenario
    if scenario == 'residue':
        assert 'user files remain after cleanup: Documents/account' in result[1], result
    if scenario == 'reopened':
        assert 'game_reopened_during_cleanup' in result[1], result
    if scenario in ['running', 'bad_container', 'old_stage', 'old_failure']:
        assert g.clear_calls == 0 and g.deny_writes == 0, scenario
    elif scenario == 'restore_failure':
        assert g.nodes[g.work + '/QtsVFSCache'] == 'directory', scenario
    else:
        assert g.nodes[g.root + '/' + g.resource + '/asset'] == 'file', scenario
        assert g.deny_writes == 1 and g.policy.allowedPriceQuotas['0.29'] == 0, scenario
    if scenario not in ['success', 'running', 'bad_container']:
        before = g.clear_calls
        module.clean(g.bundle)
        assert g.clear_calls == before, f'{scenario}: unsafe retry'
    if scenario == 'success':
        assert module.policy_path(g.bundle) == g.root + '/Library/Preferences/com.iapguard.runtime.plist'
        # A new user file invalidates old evidence; a clean marker is not sufficient.
        lua.execute("nodes[root..'/Documents/new-account']='file'")
        assert not module.verify(g.bundle)
    print('PASS:', scenario)
print('PASS: cleanup module simulated orchestration only; no keychain or logged-out UI verification')
