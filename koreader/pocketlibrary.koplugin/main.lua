--[[--
Pocket Library for KOReader.

Downloads the books sent to this reader from Pocket Library, sends the
highlights and the reading time of the open book to the library, and sets up
KOReader's own progress sync and OPDS catalogue for the library in one go.

The plugin signs in with the account's email and an app password made for
this reader in Pocket Library → Devices.

@module koplugin.pocketlibrary
--]]--

local DataStorage = require("datastorage")
local Device = require("device")
local InfoMessage = require("ui/widget/infomessage")
local LuaSettings = require("luasettings")
local MultiInputDialog = require("ui/widget/multiinputdialog")
local NetworkMgr = require("ui/network/manager")
local Notification = require("ui/widget/notification")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local lfs = require("libs/libkoreader-lfs")
local logger = require("logger")
local util = require("util")
local _ = require("gettext")
local T = require("ffi/util").template

local PocketLibrary = WidgetContainer:extend{
    name = "pocketlibrary",
    is_doc_only = false,
    settings_file = DataStorage:getSettingsDir() .. "/pocketlibrary.lua",
}

local VERSION = "1.0"

function PocketLibrary:init()
    self.settings = LuaSettings:open(self.settings_file)
    self.ui.menu:registerToMainMenu(self)
end

-- ---------------------------------------------------------------------------
-- settings

function PocketLibrary:get(key, default)
    local v = self.settings:readSetting(key)
    if v == nil then return default end
    return v
end

function PocketLibrary:set(key, value)
    self.settings:saveSetting(key, value)
    self.settings:flush()
end

function PocketLibrary:baseURL()
    local url = self:get("server", "")
    url = url:gsub("%s+", ""):gsub("/+$", "")
    -- people paste the catalogue or sync address: the server is the part before
    url = url:gsub("/opds$", ""):gsub("/kosync$", "")
    return url
end

function PocketLibrary:configured(quiet)
    if self:baseURL() ~= "" and self:get("username", "") ~= "" and self:get("password", "") ~= "" then
        return true
    end
    if not quiet then
        UIManager:show(InfoMessage:new{
            text = _("Set the server, your email and the app password first (Pocket Library → Server and account)."),
        })
    end
    return false
end

-- ---------------------------------------------------------------------------
-- HTTP

local function jsonEncode(v)
    local ok, JSON = pcall(require, "json")
    if ok and JSON and JSON.encode then return JSON.encode(v) end
    local rapidjson = require("rapidjson")
    return rapidjson.encode(v)
end

local function jsonDecode(s)
    local ok, JSON = pcall(require, "json")
    if ok and JSON and JSON.decode then return JSON.decode(s) end
    local rapidjson = require("rapidjson")
    return rapidjson.decode(s)
end

--- Makes a request to the server. Returns ok, data (decoded JSON, or the
-- error message when not ok). With to_file the body is written there instead.
function PocketLibrary:request(method, path, body, to_file)
    local http = require("socket.http")
    local ltn12 = require("ltn12")
    local mime = require("mime")
    local socket = require("socket")
    local socketutil = require("socketutil")

    local headers = {
        ["Accept"] = "application/json",
        ["Authorization"] = "Basic " .. mime.b64(self:get("username", "") .. ":" .. self:get("password", "")),
        ["User-Agent"] = "PocketLibrary-KOReader/" .. VERSION,
    }
    local source
    if body ~= nil then
        local data = jsonEncode(body)
        headers["Content-Type"] = "application/json"
        headers["Content-Length"] = tostring(#data)
        source = ltn12.source.string(data)
    end
    local chunks = {}
    local sink, fh
    if to_file then
        fh = io.open(to_file, "wb")
        if not fh then
            return false, T(_("Cannot write %1"), to_file)
        end
        sink = ltn12.sink.file(fh)
        socketutil:set_timeout(socketutil.FILE_BLOCK_TIMEOUT, socketutil.FILE_TOTAL_TIMEOUT)
    else
        sink = ltn12.sink.table(chunks)
        socketutil:set_timeout(socketutil.LARGE_BLOCK_TIMEOUT, socketutil.LARGE_TOTAL_TIMEOUT)
    end
    local ok, code, _headers, status = pcall(function()
        return socket.skip(1, http.request{
            url = self:baseURL() .. path,
            method = method,
            headers = headers,
            source = source,
            sink = sink,
        })
    end)
    socketutil:reset_timeout()
    if not ok then
        return false, tostring(code)
    end
    if type(code) ~= "number" then
        return false, T(_("Cannot reach the server: %1"), tostring(status or code))
    end
    if code == 401 then
        return false, _("The server refused the email or the app password.")
    end
    local text = table.concat(chunks)
    if code < 200 or code >= 300 then
        local msg = status or tostring(code)
        local okj, err = pcall(jsonDecode, text)
        if okj and type(err) == "table" and err.error then msg = err.error end
        return false, T(_("Server error: %1"), msg)
    end
    if to_file then
        return true, nil
    end
    if text == "" then
        return true, {}
    end
    local okj, data = pcall(jsonDecode, text)
    if not okj then
        return false, _("The server sent something that is not JSON.")
    end
    return true, data
end

-- whether the reader is online right now, without asking to turn Wi-Fi on
local function isOnline()
    if NetworkMgr.isOnline then return NetworkMgr:isOnline() end
    if NetworkMgr.isConnected then return NetworkMgr:isConnected() end
    return false
end

local function show(text, timeout)
    UIManager:show(InfoMessage:new{ text = text, timeout = timeout })
end

local function notify(text)
    UIManager:show(Notification:new{ text = text })
end

-- ---------------------------------------------------------------------------
-- server and account

function PocketLibrary:editAccount(touchmenu_instance)
    local dialog
    dialog = MultiInputDialog:new{
        title = _("Pocket Library"),
        fields = {
            {
                text = self:get("server", ""),
                hint = "https://library.example.com",
                description = _("Server"),
            },
            {
                text = self:get("username", ""),
                hint = _("you@example.com"),
                description = _("Email"),
            },
            {
                text = self:get("password", ""),
                hint = _("App password from Pocket Library → Devices"),
                description = _("App password"),
                text_type = "password",
            },
        },
        buttons = {
            {
                {
                    text = _("Cancel"),
                    id = "close",
                    callback = function()
                        UIManager:close(dialog)
                    end,
                },
                {
                    text = _("Save and check"),
                    is_enter_default = true,
                    callback = function()
                        local fields = dialog:getFields()
                        self:set("server", util.trim(fields[1] or ""))
                        self:set("username", util.trim(fields[2] or ""))
                        self:set("password", util.trim(fields[3] or ""))
                        UIManager:close(dialog)
                        if touchmenu_instance then touchmenu_instance:updateItems() end
                        self:checkAccount()
                    end,
                },
            },
        },
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function PocketLibrary:checkAccount()
    if not self:configured() then return end
    NetworkMgr:runWhenOnline(function()
        local ok, me = self:request("GET", "/koreader/v1/me")
        if not ok then
            show(me)
            return
        end
        local who = me.name ~= "" and me.name or me.email
        if type(me.device) == "table" and type(me.device.name) == "string" then
            show(T(_("Signed in as %1, as the device “%2”."), who, me.device.name))
        else
            show(T(_("Signed in as %1."), who))
        end
    end)
end

-- ---------------------------------------------------------------------------
-- KOReader sync and the OPDS catalogue

function PocketLibrary:setupKOSync()
    if not self:configured() then return end
    local md5 = require("ffi/sha2").md5
    local server = self:baseURL() .. "/kosync"
    local username = self:get("username")
    local userkey = md5(self:get("password"))
    local applied = false
    -- the progress sync plugin of the open book keeps its settings in memory
    local kosync = self.ui and self.ui.kosync
    if kosync and type(kosync.settings) == "table" then
        kosync.settings.custom_server = server
        kosync.settings.username = username
        kosync.settings.userkey = userkey
        kosync.updated = true
        if kosync.onFlushSettings then pcall(kosync.onFlushSettings, kosync) end
        applied = true
    end
    if not applied then
        local ok, err = pcall(function()
            local s = LuaSettings:open(DataStorage:getSettingsDir() .. "/kosync.lua")
            local t = s:readSetting("settings") or {}
            t.custom_server = server
            t.username = username
            t.userkey = userkey
            s:saveSetting("settings", t)
            s:flush()
        end)
        if not ok then logger.warn("PocketLibrary: kosync settings", err) end
    end
    -- older KOReader versions keep it with the reader's settings
    local legacy = G_reader_settings:readSetting("kosync")
    if type(legacy) == "table" then
        legacy.custom_server = server
        legacy.username = username
        legacy.userkey = userkey
        G_reader_settings:saveSetting("kosync", legacy)
    end
    show(T(_([[Progress sync now uses Pocket Library:
%1

Your place travels between this reader, the web reader and your other devices. Turn on automatic sync under Progress sync if you like.%2]]),
        server, applied and "" or "\n\n" .. _("Restart KOReader if a book was open since it started.")))
end

function PocketLibrary:addCatalog()
    if not self:configured() then return end
    local entry = {
        title = "Pocket Library",
        url = self:baseURL() .. "/opds",
        username = self:get("username"),
        password = self:get("password"),
    }
    local function upsert(servers)
        for _i, s in ipairs(servers) do
            if s.url == entry.url or s.title == entry.title then
                s.title, s.url, s.username, s.password = entry.title, entry.url, entry.username, entry.password
                return
            end
        end
        table.insert(servers, 1, entry)
    end
    local done = false
    local opds = self.ui and self.ui.opds
    if opds and opds.loadSettings then
        local ok = pcall(function()
            opds:loadSettings()
            if type(opds.servers) == "table" and opds.settings then
                upsert(opds.servers)
                opds.settings:saveSetting("servers", opds.servers)
                opds.settings:flush()
                done = true
            end
        end)
        if not ok then done = false end
    end
    if not done then
        local ok, err = pcall(function()
            local s = LuaSettings:open(DataStorage:getSettingsDir() .. "/opds.lua")
            local servers = s:readSetting("servers")
            if type(servers) ~= "table" then
                servers = {}
            end
            upsert(servers)
            s:saveSetting("servers", servers)
            s:flush()
        end)
        if not ok then logger.warn("PocketLibrary: opds settings", err) end
    end
    local legacy = G_reader_settings:readSetting("opds_servers")
    if type(legacy) == "table" then
        upsert(legacy)
        G_reader_settings:saveSetting("opds_servers", legacy)
    end
    show(_("Pocket Library is now in the OPDS catalog list (file browser → Search → OPDS catalog)."))
end

-- ---------------------------------------------------------------------------
-- books sent to this reader

function PocketLibrary:downloadDir()
    local dir = self:get("download_dir")
    if dir and lfs.attributes(dir, "mode") == "directory" then
        return dir
    end
    local home = G_reader_settings:readSetting("home_dir") or Device.home_dir or DataStorage:getDataDir()
    return home .. "/Pocket Library"
end

function PocketLibrary:chooseDownloadDir(touchmenu_instance)
    local DownloadMgr = require("ui/downloadmgr")
    DownloadMgr:new{
        onConfirm = function(path)
            self:set("download_dir", path)
            if touchmenu_instance then touchmenu_instance:updateItems() end
        end,
    }:chooseDir(self:downloadDir())
end

local function refreshFileManager()
    local ok, FileManager = pcall(require, "apps/filemanager/filemanager")
    if ok and FileManager and FileManager.instance then
        FileManager.instance:onRefresh()
    end
end

function PocketLibrary:downloadQueue(quiet)
    if not self:configured(quiet) then return end
    local work = function()
        local ok, items = self:request("GET", "/koreader/v1/queue")
        if not ok then
            if not quiet then show(items) end
            return
        end
        if type(items) ~= "table" or #items == 0 then
            if not quiet then show(_("Nothing is waiting for this reader."), 3) end
            return
        end
        local dir = self:downloadDir()
        if lfs.attributes(dir, "mode") ~= "directory" then
            util.makePath(dir)
        end
        local got, failed = 0, {}
        for _i, item in ipairs(items) do
            local name = util.getSafeFilename(item.filename or ("book-" .. tostring(item.book_id)), dir)
            local path = dir .. "/" .. name
            local part = path .. ".part"
            local okd, err = self:request("GET", item.url, nil, part)
            if okd and lfs.attributes(part, "size") and lfs.attributes(part, "size") > 0 then
                os.remove(path)
                os.rename(part, path)
                self:request("POST", "/koreader/v1/queue/" .. tostring(item.id) .. "/done", {})
                got = got + 1
            else
                os.remove(part)
                table.insert(failed, (item.title or name) .. ": " .. tostring(err))
            end
        end
        refreshFileManager()
        if #failed > 0 then
            show(T(_("Downloaded %1 of %2 books.\n\n%3"), got, #items, table.concat(failed, "\n")))
        elseif got > 0 then
            if quiet then
                notify(T(_("Pocket Library: %1 new books"), got))
            else
                show(T(_("Downloaded %1 books to %2."), got, dir), 4)
            end
        end
    end
    if quiet then
        if isOnline() then pcall(work) end
    else
        NetworkMgr:runWhenOnline(work)
    end
end

function PocketLibrary:onNetworkConnected()
    if self:get("auto_download", false) and self:configured(true) then
        UIManager:scheduleIn(3, function() self:downloadQueue(true) end)
    end
end

-- ---------------------------------------------------------------------------
-- the open book

function PocketLibrary:documentInfo()
    if not (self.ui and self.ui.document) then return nil end
    local doc_settings = self.ui.doc_settings
    local md5 = doc_settings and doc_settings:readSetting("partial_md5_checksum")
    if not md5 then
        md5 = util.partialMD5(self.ui.document.file)
    end
    local props = self.ui.doc_props or {}
    if (not props.title or props.title == "") and self.ui.document.getProps then
        local ok, p = pcall(self.ui.document.getProps, self.ui.document)
        if ok and p then props = p end
    end
    local pages
    local okp, n = pcall(function() return self.ui.document:getPageCount() end)
    if okp then pages = n end
    local _dir, filename = util.splitFilePathName(self.ui.document.file)
    return {
        document = md5,
        title = props.display_title or props.title or "",
        authors = props.authors or "",
        filename = filename,
        pages = pages,
    }
end

function PocketLibrary:collectAnnotations()
    local list = {}
    if self.ui.annotation and type(self.ui.annotation.annotations) == "table" then
        for _i, a in ipairs(self.ui.annotation.annotations) do
            table.insert(list, {
                datetime = a.datetime,
                datetime_updated = a.datetime_updated,
                text = a.text,
                note = a.note,
                chapter = a.chapter,
                pageno = a.pageno,
                pos0 = a.pos0,
                pos1 = a.pos1,
                page = a.page,
                color = a.color,
                drawer = a.drawer,
            })
        end
    elseif self.ui.bookmark and type(self.ui.bookmark.bookmarks) == "table" then
        -- before 2024 highlights lived in bookmarks: "notes" is the highlighted
        -- text and "text" the note
        for _i, b in ipairs(self.ui.bookmark.bookmarks) do
            local highlighted = b.highlighted
            table.insert(list, {
                datetime = b.datetime,
                text = highlighted and b.notes or nil,
                note = (b.text and b.text ~= b.notes) and b.text or nil,
                chapter = b.chapter,
                pageno = type(b.page) == "number" and b.page or nil,
                pos0 = b.pos0,
                pos1 = b.pos1,
                page = b.page,
            })
        end
    end
    return list
end

function PocketLibrary:syncHighlights(quiet)
    if not self:configured(quiet) then return end
    local info = self:documentInfo()
    if not info or not info.document then return end
    local work = function()
        local body = {
            document = info.document,
            title = info.title,
            authors = info.authors,
            filename = info.filename,
            pages = info.pages,
            complete = true,
        }
        local list = self:collectAnnotations()
        if #list > 0 then body.annotations = list end
        local ok, res = self:request("POST", "/koreader/v1/annotations", body)
        if not ok then
            if not quiet then show(res) end
            return
        end
        if not res.matched then
            if not quiet then show(_("This book is not in your Pocket Library (or not in a library you can open).")) end
            return
        end
        if not quiet then
            show(T(_("Highlights of “%1”: %2 new, %3 updated, %4 removed."), res.title or info.title, res.created or 0, res.updated or 0, res.deleted or 0), 4)
        end
    end
    if quiet then
        if isOnline() then pcall(work) end
    else
        NetworkMgr:runWhenOnline(work)
    end
end

-- reading statistics (the statistics plugin's database)

function PocketLibrary:statsRows(md5)
    local db = DataStorage:getSettingsDir() .. "/statistics.sqlite3"
    if lfs.attributes(db, "mode") ~= "file" then return nil, _("Reading statistics are off (no statistics database).") end
    if self.ui.statistics and self.ui.statistics.insertDB then
        pcall(self.ui.statistics.insertDB, self.ui.statistics)
    end
    local SQ3 = require("lua-ljsqlite3/init")
    local conn = SQ3.open(db)
    local rows = {}
    local ok, err = pcall(function()
        local stmt = conn:prepare("SELECT id FROM book WHERE md5 = ? ORDER BY last_open DESC LIMIT 1;")
        local row = stmt:reset():bind(md5):step()
        stmt:close()
        if not row then return end
        local id_book = tonumber(row[1])
        local sent = self:get("stats_sent", {})
        local since = tonumber(sent[md5] or 0) or 0
        local q = conn:prepare("SELECT page, start_time, duration, total_pages FROM page_stat_data WHERE id_book = ? AND start_time > ? ORDER BY start_time LIMIT 5000;")
        q:reset():bind(id_book, since)
        local r = q:step()
        while r do
            table.insert(rows, {
                page = tonumber(r[1]),
                start_time = tonumber(r[2]),
                duration = tonumber(r[3]),
                total_pages = tonumber(r[4]),
            })
            r = q:step()
        end
        q:close()
    end)
    conn:close()
    if not ok then return nil, tostring(err) end
    return rows
end

function PocketLibrary:sendStats(quiet)
    if not self:configured(quiet) then return end
    local info = self:documentInfo()
    if not info or not info.document then return end
    local rows, err = self:statsRows(info.document)
    if not rows then
        if not quiet then show(err) end
        return
    end
    if #rows == 0 then
        if not quiet then show(_("No new reading time for this book."), 3) end
        return
    end
    local work = function()
        local ok, res = self:request("POST", "/koreader/v1/stats", {
            document = info.document,
            title = info.title,
            authors = info.authors,
            device = Device.model,
            pages = rows,
        })
        if not ok then
            if not quiet then show(res) end
            return
        end
        if res.matched then
            local sent = self:get("stats_sent", {})
            sent[info.document] = rows[#rows].start_time
            self:set("stats_sent", sent)
        end
        if not quiet then
            if res.matched then
                show(T(_("Sent %1 reading sessions."), res.sessions or 0), 3)
            else
                show(_("This book is not in your Pocket Library."))
            end
        end
    end
    if quiet then
        if isOnline() then pcall(work) end
    else
        NetworkMgr:runWhenOnline(work)
    end
end

function PocketLibrary:onCloseDocument()
    if not self:configured(true) then return end
    if self:get("sync_on_close", false) then
        pcall(self.syncHighlights, self, true)
    end
    if self:get("stats_on_close", false) then
        pcall(self.sendStats, self, true)
    end
end

-- ---------------------------------------------------------------------------
-- menu

function PocketLibrary:addToMainMenu(menu_items)
    local function toggle(key, default)
        return {
            checked_func = function() return self:get(key, default) end,
            callback = function() self:set(key, not self:get(key, default)) end,
        }
    end
    local function item(t, extra)
        for k, v in pairs(extra or {}) do t[k] = v end
        return t
    end
    local has_doc = function() return self.ui.document ~= nil end
    menu_items.pocket_library = {
        text = _("Pocket Library"),
        sorting_hint = "tools",
        sub_item_table = {
            {
                text_func = function()
                    if self:configured(true) then
                        return T(_("Account: %1"), self:get("username"))
                    end
                    return _("Server and account…")
                end,
                keep_menu_open = true,
                callback = function(touchmenu_instance) self:editAccount(touchmenu_instance) end,
            },
            {
                text = _("Use Pocket Library for progress sync"),
                callback = function() self:setupKOSync() end,
            },
            {
                text = _("Add the catalogue to OPDS"),
                callback = function() self:addCatalog() end,
                separator = true,
            },
            {
                text = _("Download books sent to this reader"),
                callback = function() self:downloadQueue(false) end,
            },
            {
                text_func = function() return T(_("Download folder: %1"), self:downloadDir()) end,
                keep_menu_open = true,
                callback = function(touchmenu_instance) self:chooseDownloadDir(touchmenu_instance) end,
            },
            item({ text = _("Check for sent books when Wi-Fi connects"), separator = true }, toggle("auto_download", false)),
            {
                text = _("Send highlights of this book"),
                enabled_func = has_doc,
                callback = function() self:syncHighlights(false) end,
            },
            item({ text = _("Send highlights when closing a book") }, toggle("sync_on_close", false)),
            {
                text = _("Send reading time of this book"),
                enabled_func = has_doc,
                callback = function() self:sendStats(false) end,
            },
            item({ text = _("Send reading time when closing a book"), separator = true }, toggle("stats_on_close", false)),
            {
                text = _("About"),
                keep_menu_open = true,
                callback = function()
                    show(T(_([[Pocket Library plugin %1

Sign in with your email and an app password made for this reader in Pocket Library → Devices. Books you send to this reader there wait until you download them here; highlights and reading time go to the library, where the web reader, Pocket Bookshelf and your stats see them.]]), VERSION))
                end,
            },
        },
    }
end

return PocketLibrary
