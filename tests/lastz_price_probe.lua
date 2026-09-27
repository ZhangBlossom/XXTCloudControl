-- XXTouch 真机测试。默认只检查状态，不点击、不启动/关闭游戏、不付款。
-- 写配置时由调用者在本脚本前定义：
-- LASTZ_TEST_ORDER = {price = '实际商品价格', quantity = 0}
-- quantity=0 用于先验证拒绝购买；大于 0 会真实放行相应次数，不能自动执行购买。
-- 当前插件只限制价格和次数，不能凭商品名称区分同价商品。
local json = require('cjson.safe')
local bundle = 'com.readygo.barrel.ios'
-- 此路径已在当前测试手机核实；其他手机需重新定位 roothide 根目录。
local path = '/var/containers/Bundle/Application/.jbroot-2718193559BBE312/var/mobile/Library/Preferences/com.iapguard.runtime.plist'
local report_path = '/var/mobile/Media/1ferver/lua/scripts/lastz-price-probe.json'
local report = {bundle_id=bundle, purchase_interception_verified=false}
local ok, err = pcall(function()
    report.front_bundle = app.front_bid()
    report.running = app.is_running(bundle)
    report.before = assert(plist.read(path), '无法读取 IAPGuard 配置')
    local order = LASTZ_TEST_ORDER
    if order then
        assert(report.front_bundle == bundle, 'Last Z 不在前台，未修改配置')
        local price = tostring(order.price or '')
        assert(price:match('^%d+%.?%d*$') and tonumber(price) > 0, '价格必须是正数')
        local quantity = order.quantity
        assert(type(quantity) == 'number' and quantity >= 0 and quantity < math.huge
            and quantity == math.floor(quantity), '次数必须是非负整数')
        local backup = path..'.before-lastz-'..os.time()
        assert(not io.open(backup, 'rb'), '备份已存在，请稍后重试')
        local src = assert(io.open(path, 'rb'))
        local bytes = assert(src:read('*a')); src:close()
        local dst = assert(io.open(backup, 'wb'))
        assert(dst:write(bytes)); assert(dst:close())
        report.backup_path = backup
        plist.write(path, {enabled=true, allowedPriceQuotas={[price]=quantity}})
        local actual = assert(plist.read(path), '无法回读新配置')
        assert(actual.enabled == true and actual.allowedPriceQuotas[price] == quantity,
            '配置回读与请求不一致')
        assert(actual.allowedPrices == nil, '旧价格白名单未移除')
        for key in pairs(actual.allowedPriceQuotas) do
            assert(key == price, '仍存在其他价格配置')
        end
        report.configuration_verified = true
    end
    report.after = assert(plist.read(path))
end)
report.ok = ok
report.error = not ok and tostring(err) or nil
report.completed_at = os.time()
local output = assert(io.open(report_path, 'wb'))
assert(output:write(assert(json.encode(report)))); assert(output:close())
if not ok then error(err) end
