using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Text;
using System.Web.Script.Serialization;

namespace MagDownloader
{
    // ============ 云端服务配置（服务器地址，可持久化到本地配置文件） ============
    internal static class CloudConfig
    {
        private const string DefaultUrl = "http://124.222.167.203:8080";
        private static bool _loaded = false;

        // 静态可写：测试时可指向本地服务；Load() 会用配置文件覆盖一次。
        public static string ServerUrl = DefaultUrl;

        public static string ConfigFile { get { return Path.Combine(AppPaths.Data, "cloud.cfg"); } }

        public static void Load()
        {
            if (_loaded) return;
            _loaded = true;
            try
            {
                AppPaths.Ensure();
                if (File.Exists(ConfigFile))
                {
                    string s = File.ReadAllText(ConfigFile, Encoding.UTF8).Trim();
                    if (!string.IsNullOrWhiteSpace(s))
                    {
                        ServerUrl = s.TrimEnd('/');
                        Logger.App("云端服务地址：" + ServerUrl);
                    }
                }
            }
            catch (Exception ex) { Logger.App("读取云端配置失败：" + ex.Message); }
        }

        public static void Save(string url)
        {
            try
            {
                AppPaths.Ensure();
                ServerUrl = url.Trim().TrimEnd('/');
                File.WriteAllText(ConfigFile, ServerUrl, Encoding.UTF8);
            }
            catch (Exception ex) { Logger.App("写入云端配置失败：" + ex.Message); }
        }
    }

    // ============ 云端认证结果 ============
    internal class CloudResult
    {
        public bool Ok = false;
        public bool NetworkError = false;  // 服务器不可达（此时客户端回退本地账密）
        public string Token = "";          // access token
        public string RefreshToken = "";   // refresh token（服务端 30 天有效）
        public int ExpiresIn = 0;          // access token 剩余秒数，仅用于日志与排障
        public string Error = "";
        public bool IsAdmin = false;
        public int HttpStatus = 0;
    }

    // ============ 云端认证客户端：调用 Go 服务 /api/v1/auth/register 与 /login ============
    internal static class CloudAuth
    {
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        public static CloudResult Register(string user, string pass)
        {
            return Post("/api/v1/auth/register", new Cred { username = user, password = pass });
        }

        public static CloudResult Login(string user, string pass)
        {
            return Post("/api/v1/auth/login", new Cred { username = user, password = pass });
        }

        // 用 refresh token 换一对新令牌。access token 过期后由 CloudSession 调用，
        // 用户不会看到「用一会儿就 401」。
        public static CloudResult Refresh(string refreshToken)
        {
            return Post("/api/v1/auth/refresh", new RefreshReq { refresh_token = refreshToken });
        }

        private static CloudResult Post(string path, object cred)
        {
            CloudResult r = new CloudResult();
            string url = CloudConfig.ServerUrl + path;
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create(url);
                req.Method = "POST";
                req.ContentType = "application/json";
                req.Timeout = 6000;
                req.ReadWriteTimeout = 8000;
                byte[] body = Encoding.UTF8.GetBytes(Json.Serialize(cred));
                req.ContentLength = body.Length;
                using (Stream s = req.GetRequestStream()) s.Write(body, 0, body.Length);

                using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                {
                    r.HttpStatus = (int)resp.StatusCode;
                    string text = new StreamReader(resp.GetResponseStream(), Encoding.UTF8).ReadToEnd();
                    if (r.HttpStatus == 200 || r.HttpStatus == 201)
                    {
                        Dictionary<string, object> obj = Json.DeserializeObject(text) as Dictionary<string, object>;
                        if (obj != null)
                        {
                            if (obj.ContainsKey("token")) r.Token = obj["token"] as string;
                            if (obj.ContainsKey("refresh_token")) r.RefreshToken = obj["refresh_token"] as string;
                            if (obj.ContainsKey("expires_in"))
                            {
                                int ei;
                                if (int.TryParse(Convert.ToString(obj["expires_in"]), out ei)) r.ExpiresIn = ei;
                            }
                            if (obj.ContainsKey("user"))
                            {
                                Dictionary<string, object> u = obj["user"] as Dictionary<string, object>;
                                if (u != null && u.ContainsKey("is_admin")) r.IsAdmin = Convert.ToBoolean(u["is_admin"]);
                            }
                        }
                        r.Ok = !string.IsNullOrEmpty(r.Token);
                        if (!r.Ok) r.Error = "云端返回异常";
                    }
                    else
                    {
                        r.Error = ParseError(text);
                    }
                }
            }
            catch (WebException we)
            {
                HttpWebResponse resp = we.Response as HttpWebResponse;
                if (resp != null)
                {
                    r.HttpStatus = (int)resp.StatusCode;
                    try
                    {
                        using (StreamReader sr = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                            r.Error = ParseError(sr.ReadToEnd());
                    }
                    catch { r.Error = "云端认证失败"; }
                }
                else
                {
                    r.NetworkError = true;
                    r.Error = "无法连接云端服务（" + CloudConfig.ServerUrl + "）";
                }
            }
            catch (Exception ex)
            {
                r.NetworkError = true;
                r.Error = "网络错误：" + ex.Message;
            }
            return r;
        }

        private static string ParseError(string json)
        {
            try
            {
                Dictionary<string, object> obj = Json.DeserializeObject(json) as Dictionary<string, object>;
                if (obj != null && obj.ContainsKey("error")) return obj["error"] as string;
            }
            catch { }
            return "云端认证失败";
        }

        private class Cred { public string username; public string password; }
        private class RefreshReq { public string refresh_token; }
    }

    // ============ 令牌会话：登录结果落盘到 Session，过期时静默续期 ============
    //
    // 多个后台线程会同时调云端 API（轮询、取回、删除）。若各自发现 401 就各自去打
    // refresh，会在令牌过期那一刻打出一串重复请求，所以这里用一把锁串行化。
    internal static class CloudSession
    {
        private static readonly object Gate = new object();
        private static DateTime _retryAfter = DateTime.MinValue;

        // 把一次成功的认证结果写进 Session（登录 / 注册 / 续期共用）。
        public static void Adopt(CloudResult r)
        {
            if (r == null) return;
            if (!string.IsNullOrEmpty(r.Token)) Session.Token = r.Token;
            if (!string.IsNullOrEmpty(r.RefreshToken)) Session.RefreshToken = r.RefreshToken;
            Session.IsAdmin = r.IsAdmin;
        }

        // 清空云端会话（退出登录、或 refresh token 也失效时）。
        public static void Clear()
        {
            Session.Token = "";
            Session.RefreshToken = "";
        }

        // 静默续期；成功返回 true。任何失败都只返回 false，不抛异常——
        // 调用方（CloudApi）会据此决定是重试还是把错误抛给用户。
        public static bool Renew()
        {
            lock (Gate)
            {
                if (string.IsNullOrEmpty(Session.RefreshToken)) return false;
                // 刚失败过就不再连打：一次断网不该引发雪崩式重试。
                if (DateTime.Now < _retryAfter) return false;

                CloudResult r;
                try { r = CloudAuth.Refresh(Session.RefreshToken); }
                catch (Exception ex) { Logger.App("令牌续期异常：" + ex.Message); r = null; }

                if (r != null && r.Ok)
                {
                    Adopt(r);
                    _retryAfter = DateTime.MinValue;
                    Logger.App("云端令牌已自动续期");
                    return true;
                }

                _retryAfter = DateTime.Now.AddSeconds(10);
                if (r != null && r.HttpStatus == 401)
                {
                    // refresh token 本身也过期了，再试也没用，直接清掉。
                    Logger.App("refresh token 已失效，需要重新登录");
                    Clear();
                }
                return false;
            }
        }
    }

    // ============ 服务端版本探测 ============
    //
    // /healthz 无需登录即可访问，用于回答「我连的这个服务端是哪一版」。
    // 探测失败不是错误：服务端可能暂时不可达，界面照常用。
    internal static class CloudVersion
    {
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        public static string Fetch()
        {
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create(CloudConfig.ServerUrl + "/healthz");
                req.Method = "GET";
                req.Timeout = 6000;
                req.ReadWriteTimeout = 6000;
                using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                using (StreamReader sr = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                {
                    Dictionary<string, object> d = Json.DeserializeObject(sr.ReadToEnd()) as Dictionary<string, object>;
                    if (d != null && d.ContainsKey("version") && d["version"] != null)
                        return d["version"].ToString();
                }
            }
            catch { }
            return "";
        }
    }

    // ============ 云端下载任务（服务端离线下载） ============
    internal class CloudTask
    {
        public string InfoHash = "";
        public string Name = "";
        public string Status = "";
        public long Total = 0, Completed = 0, Speed = 0;
        public int Peers = 0;
    }

    // ============ 云端下载 API：把磁力交给服务器离线下载，再取回本地 ============
    internal static class CloudApi
    {
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        private static void CheckAuth()
        {
            if (string.IsNullOrEmpty(Session.Token))
                throw new Exception("尚未登录云端，无法使用云端下载");
        }

        private static Dictionary<string, object> Api(string method, string path, object body)
        {
            CheckAuth();
            try
            {
                return ApiOnce(method, path, body);
            }
            catch (WebException we)
            {
                // 只在 401 时续期重试；网络错误原样抛出，避免把「服务器没开」误判成登录过期。
                if (!IsUnauthorized(we) || !CloudSession.Renew()) throw;
                return ApiOnce(method, path, body);
            }
        }

        private static bool IsUnauthorized(WebException we)
        {
            HttpWebResponse resp = we.Response as HttpWebResponse;
            if (resp == null) return false;
            try { return (int)resp.StatusCode == 401; }
            finally { resp.Close(); }
        }

        private static Dictionary<string, object> ApiOnce(string method, string path, object body)
        {
            string url = CloudConfig.ServerUrl + path;
            HttpWebRequest req = (HttpWebRequest)WebRequest.Create(url);
            req.Method = method;
            req.ContentType = "application/json";
            req.Timeout = 20000;
            req.ReadWriteTimeout = 20000;
            req.Headers["Authorization"] = "Bearer " + Session.Token;
            if (body != null)
            {
                byte[] b = Encoding.UTF8.GetBytes(Json.Serialize(body));
                req.ContentLength = b.Length;
                using (Stream s = req.GetRequestStream()) s.Write(b, 0, b.Length);
            }
            using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
            using (StreamReader sr = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
            {
                return Json.DeserializeObject(sr.ReadToEnd()) as Dictionary<string, object>;
            }
        }

        public static string AddTorrent(string magnet)
        {
            Dictionary<string, object> d = Api("POST", "/api/v1/torrents",
                new Dictionary<string, object> { { "magnet", magnet } });
            if (d != null && d.ContainsKey("info_hash")) return d["info_hash"] as string;
            if (d != null && d.ContainsKey("error")) throw new Exception(d["error"] as string);
            throw new Exception("云端返回异常");
        }

        public static List<CloudTask> ListTorrents()
        {
            List<CloudTask> list = new List<CloudTask>();
            Dictionary<string, object> d;
            try { d = Api("GET", "/api/v1/torrents", null); }
            catch { return list; }
            if (d == null || !d.ContainsKey("tasks")) return list;
            object[] arr = d["tasks"] as object[];
            if (arr == null) return list;
            foreach (object o in arr)
            {
                Dictionary<string, object> t = o as Dictionary<string, object>;
                if (t == null) continue;
                CloudTask ct = new CloudTask();
                ct.InfoHash = S(t, "info_hash");
                ct.Name = S(t, "name");
                ct.Status = S(t, "status");
                ct.Total = L(t, "total");
                ct.Completed = L(t, "completed");
                ct.Speed = L(t, "speed");
                ct.Peers = (int)L(t, "peers");
                list.Add(ct);
            }
            return list;
        }

        public static void DeleteTorrent(string infoHash)
        {
            Api("DELETE", "/api/v1/torrents/" + Uri.EscapeDataString(infoHash), null);
        }

        public static void DownloadFile(string infoHash, string savePath)
        {
            CheckAuth();
            try
            {
                DownloadFileOnce(infoHash, savePath);
            }
            catch (WebException we)
            {
                // 大文件下载前先确认令牌：401 时续期后重下一次（此时尚未写入任何字节）
                if (!IsUnauthorized(we) || !CloudSession.Renew()) throw;
                DownloadFileOnce(infoHash, savePath);
            }
        }

        private static void DownloadFileOnce(string infoHash, string savePath)
        {
            string url = CloudConfig.ServerUrl + "/api/v1/torrents/" + Uri.EscapeDataString(infoHash) + "/file";
            HttpWebRequest req = (HttpWebRequest)WebRequest.Create(url);
            req.Method = "GET";
            req.Timeout = 60000;
            req.ReadWriteTimeout = 600000;
            req.Headers["Authorization"] = "Bearer " + Session.Token;
            using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
            using (Stream rs = resp.GetResponseStream())
            using (FileStream fs = new FileStream(savePath, FileMode.Create, FileAccess.Write))
            {
                byte[] buf = new byte[65536];
                int n;
                while ((n = rs.Read(buf, 0, buf.Length)) > 0) fs.Write(buf, 0, n);
            }
        }

        private static string S(Dictionary<string, object> d, string k)
        { return d.ContainsKey(k) && d[k] != null ? d[k].ToString() : ""; }

        private static long L(Dictionary<string, object> d, string k)
        {
            if (!d.ContainsKey(k) || d[k] == null) return 0;
            object o = d[k];
            if (o is long) return (long)o;
            if (o is int) return (int)o;
            if (o is double) return (long)(double)o;
            long v; long.TryParse(o.ToString(), out v); return v;
        }
    }
}