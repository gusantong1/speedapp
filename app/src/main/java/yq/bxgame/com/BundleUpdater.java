package yq.bxgame.com;

import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.*;
import java.util.regex.Pattern;
import java.util.zip.*;
import java.util.concurrent.TimeUnit;
import okhttp3.*;

/** 不依赖 Android 的更新核心，便于用真实 HTTP/文件系统独立验证。 */
public final class BundleUpdater {
    public static final String BUILTIN = "builtin";
    private static final Pattern VERSION = Pattern.compile("[A-Za-z0-9][A-Za-z0-9._-]{0,63}");
    private static final Pattern ID = Pattern.compile("[a-f0-9]{32}");
    public static final class Options {
        public Resolver resolver = host -> Arrays.asList(InetAddress.getAllByName(host));
        public final List<String> allowedOrigins = new ArrayList<>();
        public final List<String> allowedPrivateCidrs = new ArrayList<>();
        public boolean allowHttp;
        public int timeoutSec = 120, maxEntries = 20000;
        public long maxZipBytes = 128L<<20, maxExpandedBytes = 512L<<20, maxFileBytes = 64L<<20, maxCompressionRatio = 200;
    }
    public interface Resolver { List<InetAddress> lookup(String host) throws UnknownHostException; }
    public static final class VersionInfo {
        public final String version, lastVersionUrl, sha256;
        public VersionInfo(String version, String url, String sha256) { this.version=version; this.lastVersionUrl=url; this.sha256=sha256; }
    }
    public interface VersionReader { VersionInfo parse(String json) throws Exception; }
    public static final class State {
        public final String id, version;
        public State(String id, String version) { this.id=id; this.version=version; }
    }
    private final File root, releases;
    private final String builtinVersion;
    private final Options options;
    private final OkHttpClient http;
    private volatile State current;
    private long sequence;

    public BundleUpdater(File root, String builtinVersion, Options options) throws IOException {
        this.root=root; this.releases=new File(root,"releases"); this.builtinVersion=builtinVersion; this.options=options;
        this.http = new OkHttpClient.Builder().proxy(Proxy.NO_PROXY)
            .followRedirects(false).followSslRedirects(false)
            .connectTimeout(Math.min(20, options.timeoutSec), TimeUnit.SECONDS)
            .readTimeout(Math.min(20, options.timeoutSec), TimeUnit.SECONDS)
            .callTimeout(options.timeoutSec, TimeUnit.SECONDS)
            .dns(host -> {
                List<InetAddress> addresses = options.resolver.lookup(host);
                if (addresses.isEmpty()) throw new UnknownHostException("Empty DNS result");
                try { validateAddresses(addresses); }
                catch (Exception blocked) { throw new UnknownHostException("Destination address blocked"); }
                // OkHttp connects directly to these addresses; it does not resolve the hostname again.
                return addresses;
            }).build();
        if (!VERSION.matcher(builtinVersion).matches()) throw new IOException("Invalid builtin version");
        mkdir(releases);
        this.current=restore();
    }
    public State current() { return current; }

    /** 每个启动周期调用一次。各后端按 dominList 顺序尝试，全部失败才回退。 */
    public synchronized boolean checkAndUpdate(List<String> endpoints, VersionReader reader) throws Exception {
        Exception last=null;
        for (String endpoint : endpoints) {
            try {
                URL base=webUrl(endpoint);
                String separator=endpoint.contains("?")?"&":"?";
                URL request=webUrl(endpoint+separator+"current_version="+URLEncoder.encode(current.version,"UTF-8"));
                byte[] body=fetch(request,Collections.singletonList(origin(base)),65536);
                VersionInfo latest=reader.parse(new String(body,StandardCharsets.UTF_8));
                if (latest==null || !VERSION.matcher(latest.version).matches()) throw new IOException("Invalid version response");
                if (latest.version.equals(current.version)) return false;
                install(latest);
                return true;
            } catch (Exception failure) { last=failure; }
        }
        if (last!=null) throw new IOException("All version endpoints failed; current bundle retained",last);
        return false;
    }

    public synchronized void install(VersionInfo latest) throws Exception {
        if (latest==null || !VERSION.matcher(latest.version).matches() || latest.sha256==null || !latest.sha256.matches("[a-fA-F0-9]{64}")) throw new IOException("Version and ZIP SHA-256 are required");
        if (latest.version.equals(current.version)) return;
        File archive=File.createTempFile(".download-",".zip",root);
        File stage=new File(root,".stage-"+uuid());
        try {
            download(webUrl(latest.lastVersionUrl),archive);
            if (!digest(archive).equalsIgnoreCase(latest.sha256)) throw new IOException("ZIP SHA-256 mismatch");
            validateCentralDirectory(archive);
            mkdir(stage);
            File html=new File(stage,"html");
            mkdir(html);
            Properties manifest=extract(archive,html);
            writeProperties(new File(stage,"files.properties"),manifest);
            String id=uuid();
            File destination=new File(releases,id);
            if (!stage.renameTo(destination)) throw new IOException("Cannot publish validated release");
            // 只发布全新指针文件，不覆盖旧指针；崩溃时仍可恢复上一版。
            Properties pointer=new Properties();
            pointer.setProperty("id",id); pointer.setProperty("version",latest.version);
            File tmp=File.createTempFile(".pointer-",".tmp",root);
            try {
                writeProperties(tmp,pointer);
                File commit=new File(root,"active-"+(++sequence)+".properties");
                if (commit.exists() || !tmp.renameTo(commit)) throw new IOException("Cannot atomically switch release");
            } finally { if (tmp.exists()) tmp.delete(); }
            current=new State(id,latest.version);
        } finally {
            archive.delete();
            deleteTree(stage);
        }
    }

    public File resource(String generation,String path) throws IOException {
        if (!ID.matcher(generation).matches()) throw new IOException("Invalid resource generation");
        return child(new File(new File(releases,generation),"html"),safeName(path));
    }

    private State restore() throws IOException {
        File[] pointers=root.listFiles((dir,name)->name.matches("active-[0-9]+\\.properties"));
        if (pointers==null) throw new IOException("Cannot read bundle state");
        Arrays.sort(pointers,(a,b)->Long.compare(number(b),number(a)));
        State selected=null;
        Set<String> retained=new HashSet<>();
        Set<File> retainedPointers=new HashSet<>();
        for (File pointer:pointers) {
            sequence=Math.max(sequence,number(pointer));
            try {
                Properties p=readProperties(pointer,4096);
                String id=p.getProperty("id",""),version=p.getProperty("version","");
                if (!ID.matcher(id).matches() || !VERSION.matcher(version).matches() || !validRelease(id)) continue;
                if (selected==null) selected=new State(id,version);
                if (retained.size()<2) { retained.add(id); retainedPointers.add(pointer); }
            } catch (IOException | RuntimeException ignored) { /* 损坏的最新版不妨碍读取旧版。 */ }
        }
        // 仅在进程首次恢复时清理，正在显示的旧 generation 在本次启动期间保持不变。
        File[] children=root.listFiles();
        if (children!=null) for (File f:children) {
            if (f.getName().startsWith(".stage-") || f.getName().startsWith(".download-") || f.getName().startsWith(".pointer-")) deleteTree(f);
        }
        File[] oldReleases=releases.listFiles();
        if (oldReleases!=null) for (File f:oldReleases) if (!retained.contains(f.getName())) deleteTree(f);
        for (File f:pointers) if (!retainedPointers.contains(f)) f.delete();
        return selected==null ? new State(BUILTIN,builtinVersion):selected;
    }

    private boolean validRelease(String id) throws IOException {
        File release=new File(releases,id),html=new File(release,"html");
        Properties p=readProperties(new File(release,"files.properties"),32L<<20);
        if (p.isEmpty() || p.size()>options.maxEntries || !p.containsKey("index.html")) return false;
        long total=0;
        for (String name:p.stringPropertyNames()) {
            File f=child(html,safeName(name));
            String[] parts=p.getProperty(name).split(":",-1);
            if (parts.length!=2 || !parts[0].matches("[a-f0-9]{64}")) return false;
            long size;
            try { size=Long.parseLong(parts[1]); } catch (NumberFormatException e) { return false; }
            if (size<0 || size>options.maxFileBytes || !f.isFile() || f.length()!=size || !digest(f).equals(parts[0])) return false;
            total+=size;
            if (total>options.maxExpandedBytes) return false;
        }
        return new File(html,"index.html").length()>0;
    }

    private Properties extract(File archive,File html) throws Exception {
        Properties manifest=new Properties();
        Set<String> seen=new HashSet<>();
        long total=0;
        int count=0;
        try (ZipFile zip=new ZipFile(archive)) {
            Enumeration<? extends ZipEntry> entries=zip.entries();
            while (entries.hasMoreElements()) {
                ZipEntry entry=entries.nextElement();
                if (++count>options.maxEntries) throw new IOException("Too many ZIP entries");
                String raw=entry.getName();
                String name=safeName(raw.endsWith("/")?raw.substring(0,raw.length()-1):raw);
                if (!seen.add(name.toLowerCase(Locale.ROOT))) throw new IOException("Duplicate ZIP path");
                File file=child(html,name);
                if (entry.isDirectory()) { mkdir(file); continue; }
                long size=entry.getSize(),compressed=entry.getCompressedSize();
                if (size<0 || size>options.maxFileBytes || size>options.maxExpandedBytes-total || (size>0 && (compressed<=0 || size/compressed>options.maxCompressionRatio))) throw new IOException("ZIP expansion limit exceeded");
                mkdir(file.getParentFile());
                if (!file.createNewFile()) throw new IOException("ZIP path collision");
                MessageDigest hash=MessageDigest.getInstance("SHA-256");
                CRC32 crc=new CRC32();
                long actual=0;
                try (InputStream in=zip.getInputStream(entry); FileOutputStream out=new FileOutputStream(file)) {
                    byte[] buffer=new byte[32768]; int n;
                    while ((n=in.read(buffer))!=-1) {
                        actual+=n;
                        if (actual>size || actual>options.maxFileBytes) throw new IOException("ZIP file exceeds declared size");
                        hash.update(buffer,0,n); crc.update(buffer,0,n); out.write(buffer,0,n);
                    }
                    out.getFD().sync();
                }
                if (actual!=size || crc.getValue()!=entry.getCrc()) throw new IOException("ZIP size/CRC mismatch");
                total+=actual;
                manifest.setProperty(name,hex(hash.digest())+":"+actual);
            }
        }
        if (!manifest.containsKey("index.html") || new File(html,"index.html").length()==0) throw new IOException("ZIP must contain nonempty root index.html");
        return manifest;
    }

    // ZipFile 不暴露 Unix 类型位；读取中央目录，显式拒绝链接、特殊文件、加密及多卷/ZIP64。
    private void validateCentralDirectory(File archive) throws IOException {
        try (RandomAccessFile f=new RandomAccessFile(archive,"r")) {
            long end=f.length(),eocd=-1;
            for (long p=end-22;p>=Math.max(0,end-65557);p--) {
                f.seek(p);
                if (u32(f)==0x06054b50L) {
                    f.seek(p+20);
                    if (p+22+u16(f)==end) { eocd=p; break; }
                }
            }
            if (eocd<0) throw new IOException("Missing ZIP central directory");
            f.seek(eocd+4);
            int disk=u16(f),startDisk=u16(f),onDisk=u16(f),count=u16(f);
            long length=u32(f),start=u32(f);
            if (disk!=0 || startDisk!=0 || count!=onDisk || count==65535 || count>options.maxEntries || start+length!=eocd) throw new IOException("Unsupported ZIP layout");
            long p=start;
            for (int i=0;i<count;i++) {
                f.seek(p);
                if (u32(f)!=0x02014b50L) throw new IOException("Invalid ZIP central entry");
                f.seek(p+8); int flags=u16(f),method=u16(f);
                f.seek(p+28); int name=u16(f),extra=u16(f),comment=u16(f),diskStart=u16(f);
                f.seek(p+38); long external=u32(f); int mode=(int)(external>>>16)&0170000;
                if ((flags&1)!=0 || (method!=0 && method!=8) || diskStart!=0 || (mode!=0 && mode!=0100000 && mode!=0040000)) throw new IOException("ZIP encryption/links/special files are forbidden");
                p+=46L+name+extra+comment;
                if (p>eocd) throw new IOException("Truncated ZIP central directory");
            }
            if (p!=eocd) throw new IOException("ZIP central directory mismatch");
        }
    }

    private byte[] fetch(URL url,List<String> allowed,long limit) throws Exception {
        ByteArrayOutputStream out=new ByteArrayOutputStream();
        transfer(url,allowed,limit,out);
        return out.toByteArray();
    }
    private void download(URL url,File file) throws Exception {
        try (FileOutputStream out=new FileOutputStream(file)) {
            transfer(url,options.allowedOrigins,options.maxZipBytes,out);
            out.getFD().sync();
        }
    }
    private void transfer(URL url,List<String> allowed,long limit,OutputStream out) throws Exception {
        long deadline=System.nanoTime()+options.timeoutSec*1000000000L;
        for (int redirects=0;redirects<4;redirects++) {
            validateOrigin(url,allowed);
            Request request = new Request.Builder().url(url).header("Accept-Encoding", "identity").build();
            Call call=http.newCall(request);
            long remaining=deadline-System.nanoTime();
            if (remaining<=0) throw new IOException("Download time limit exceeded");
            call.timeout().timeout(remaining,TimeUnit.NANOSECONDS);
            try (Response response = call.execute()) {
                int status=response.code();
                if (status==301 || status==302 || status==303 || status==307 || status==308) {
                    String location=response.header("Location");
                    if (location==null) throw new IOException("Missing redirect location");
                    url=webUrl(new URL(url,location).toString());
                    continue;
                }
                ResponseBody body=response.body();
                if (status!=200 || body==null || body.contentLength()>limit) throw new IOException("Download HTTP status/size rejected");
                long total=0,declared=body.contentLength();
                try (InputStream in=body.byteStream()) {
                    byte[] buffer=new byte[32768]; int n;
                    while ((n=in.read(buffer))!=-1) {
                        total+=n;
                        if (total>limit || System.nanoTime()>deadline) throw new IOException("Download size/time limit exceeded");
                        out.write(buffer,0,n);
                    }
                }
                if (total==0 || (declared>=0 && total!=declared)) throw new IOException("Incomplete download");
                return;
            }
        }
        throw new IOException("Too many redirects");
    }

    private URL webUrl(String raw) throws Exception {
        if (raw==null || raw.length()>2048 || !raw.equals(raw.trim())) throw new IOException("Invalid URL");
        URL u=new URL(raw);
        if ((!u.getProtocol().equals("https") && !(options.allowHttp && u.getProtocol().equals("http"))) || u.getHost().isEmpty() || u.getUserInfo()!=null || u.getRef()!=null || u.getHost().endsWith(".")) throw new IOException("URL is not allowed");
        return u;
    }
    private void validateOrigin(URL url,List<String> allowed) throws Exception {
        boolean matches=false;
        for (String raw:allowed) if (origin(webUrl(raw)).equals(origin(url))) { matches=true; break; }
        if (!matches) throw new IOException("Destination origin is not allowed");
        // OkHttp may bypass Dns for literals, so validate those before creating a connection too.
        if (url.getHost().matches("[0-9.]+") || url.getHost().contains(":")) validateAddresses(Collections.singletonList(InetAddress.getByName(url.getHost())));
    }
    private void validateAddresses(List<InetAddress> addresses) throws Exception {
        for (InetAddress address:addresses) {
            boolean explicit=false;
            for (String cidr:options.allowedPrivateCidrs) if (inCidr(address,cidr)) { explicit=true; break; }
            if (explicit) continue;
            if (address.isAnyLocalAddress() || address.isLoopbackAddress() || address.isLinkLocalAddress() || address.isSiteLocalAddress() || address.isMulticastAddress()) throw new IOException("Destination address is blocked");
            for (String cidr:RESERVED) if (inCidr(address,cidr)) throw new IOException("Reserved destination address is blocked");
        }
    }
    private static final String[] RESERVED={"0.0.0.0/8","100.64.0.0/10","192.0.0.0/24","192.0.2.0/24","198.18.0.0/15","198.51.100.0/24","203.0.113.0/24","224.0.0.0/3","::/96","64:ff9b::/96","64:ff9b:1::/48","100::/64","2001::/32","2001:db8::/32","2002::/16","fc00::/7"};
    private static boolean inCidr(InetAddress address,String cidr) throws Exception {
        String[] parts=cidr.split("/");
        byte[] candidate=address.getAddress(),network=InetAddress.getByName(parts[0]).getAddress();
        int bits=Integer.parseInt(parts[1]);
        if (candidate.length!=network.length || bits<0 || bits>network.length*8) return false;
        for (int i=0;i<bits;i++) if ((candidate[i/8]&(128>>(i%8)))!=(network[i/8]&(128>>(i%8)))) return false;
        return true;
    }
    private static String origin(URL u) { return u.getProtocol()+"://"+u.getHost().toLowerCase(Locale.ROOT)+":"+(u.getPort()==-1?u.getDefaultPort():u.getPort()); }
    private static int u16(RandomAccessFile f) throws IOException { return f.readUnsignedByte() | (f.readUnsignedByte()<<8); }
    private static long u32(RandomAccessFile f) throws IOException { return (long)u16(f) | ((long)u16(f)<<16); }
    private static String safeName(String name) throws IOException {
        if (name.isEmpty() || name.length()>1024 || name.startsWith("/") || name.indexOf('\\')>=0 || name.indexOf(':')>=0 || name.indexOf(0)>=0) throw new IOException("Unsafe ZIP/resource path");
        for (String part:name.split("/",-1)) if (part.isEmpty() || part.equals(".") || part.equals("..") || part.endsWith(".") || part.endsWith(" ")) throw new IOException("Unsafe ZIP/resource segment");
        return name;
    }
    private static File child(File parent,String name) throws IOException {
        File result=new File(parent,name);
        if (!result.getCanonicalPath().startsWith(parent.getCanonicalPath()+File.separator)) throw new IOException("Resource escapes content root");
        return result;
    }
    private static long number(File pointer) {
        try { return Long.parseLong(pointer.getName().substring(7,pointer.getName().length()-11)); }
        catch (RuntimeException e) { return 0; }
    }
    private static Properties readProperties(File file,long limit) throws IOException {
        if (!file.isFile() || file.length()>limit) throw new IOException("Invalid manifest/state file");
        Properties p=new Properties();
        try (InputStream in=new FileInputStream(file)) { p.load(in); }
        return p;
    }
    private static void writeProperties(File file,Properties p) throws IOException {
        try (FileOutputStream out=new FileOutputStream(file)) { p.store(out,null); out.getFD().sync(); }
    }
    private static String digest(File file) throws IOException {
        try {
            MessageDigest hash=MessageDigest.getInstance("SHA-256");
            try (InputStream in=new FileInputStream(file)) { byte[] b=new byte[32768]; int n; while ((n=in.read(b))!=-1) hash.update(b,0,n); }
            return hex(hash.digest());
        } catch (java.security.NoSuchAlgorithmException impossible) { throw new IOException(impossible); }
    }
    private static String hex(byte[] b) { StringBuilder s=new StringBuilder(); for (byte value:b) s.append(String.format(Locale.ROOT,"%02x",value&255)); return s.toString(); }
    private static String uuid() { return UUID.randomUUID().toString().replace("-",""); }
    private static void mkdir(File f) throws IOException { if (!f.isDirectory() && !f.mkdirs()) throw new IOException("Cannot create content directory"); }
    private static void deleteTree(File f) throws IOException {
        if (!f.exists()) return;
        // 只处理应用私有目录；不沿符号链接递归。
        if (!f.getCanonicalFile().equals(f.getAbsoluteFile())) { if (!f.delete()) throw new IOException("Cannot remove invalid link"); return; }
        File[] files=f.listFiles();
        if (files!=null) for (File child:files) deleteTree(child);
        if (!f.delete()) throw new IOException("Cannot remove stale content");
    }
}
