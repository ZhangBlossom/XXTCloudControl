local json = require('cjson.safe')
local lfs = require('lfs')
local base = '/var/mobile/Media/1ferver/lua/scripts/xxt-device-smoke-20260924'
local report = {kind='non_purchase_device_smoke', checks={}, inventory={}}
local function check(name, fn)
 local ok, value = pcall(fn)
 report.checks[name] = {ok=ok, result=ok and value or tostring(value)}
end
check('plist_integer_quota', function()
 plist.write(base..'.plist', {enabled=true, allowedPriceQuotas={['648']=5}})
 local p=assert(plist.read(base..'.plist'))
 assert(p.enabled == true and p.allowedPriceQuotas['648']==5)
 return p
end)
check('plist_decimal_and_replace', function()
 plist.write(base..'.plist', {enabled=true, allowedPriceQuotas={['1.98']=2}})
 local p=assert(plist.read(base..'.plist'))
 assert(p.enabled == true and p.allowedPriceQuotas['1.98']==2 and p.allowedPriceQuotas['648']==nil)
 return p
end)
check('plist_zero_quota', function()
 plist.write(base..'.plist', {enabled=true, allowedPriceQuotas={['1.98']=0}})
 local p=assert(plist.read(base..'.plist'))
 assert(p.enabled == true and p.allowedPriceQuotas['1.98']==0)
 return p
end)
local roots={'', '/var/jb'}
if type(jbroot)=='function' then
 local ok,p=pcall(jbroot,'/')
 if ok then report.jbroot=p; roots[#roots+1]=p:gsub('/$','') end
end
local ok, iter, obj = pcall(lfs.dir, '/var/containers/Bundle/Application')
if ok then for name in iter,obj do
 if name:match('^%.jbroot%-') then roots[#roots+1]='/var/containers/Bundle/Application/'..name end
end end
for _,root in ipairs(roots) do
 for _,suffix in ipairs({'/Library/MobileSubstrate/DynamicLibraries/zzzIAPGuard.dylib','/Library/MobileSubstrate/DynamicLibraries/zzzIAPGuard.plist','/var/mobile/Library/Preferences/com.iapguard.runtime.plist','/var/lib/dpkg/status'}) do
  local p=root..suffix
  if lfs.attributes(p) then
   local entry={path=p, exists=true}
   if suffix:match('%.plist$') then
    local success,v=pcall(plist.read,p); entry.plist_readable=success and type(v)=='table'
    if success and type(v)=='table' and v.Filter then entry.filter=v.Filter end
   end
   report.inventory[#report.inventory+1]=entry
  end
 end
end
report.completed_at=os.time()
local f=assert(io.open(base..'.json','wb')); assert(f:write(assert(json.encode(report)))); f:close()
