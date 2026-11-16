import yq.bxgame.com.BundleUpdater;
import yq.bxgame.com.ContentSession;
import com.sun.net.httpserver.HttpServer;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.MessageDigest;
import java.util.*;
import java.util.concurrent.atomic.*;
import java.util.regex.*;
import java.util.zip.*;

/** 运行生产更新核心，使用真实回环 HTTP；不依赖设备或伪造 APK。 */
public final class BundleUpdaterCheck {
    private static int passed;
    private static String origin;
    private static final AtomicReference<byte[]> archive=new AtomicReference<>();
    private static final AtomicReference<String> metadata=new AtomicReference<>();
    private static final AtomicInteger downloads=new AtomicInteger(), versionRequests=new AtomicInteger();
    private static final AtomicReference<String> requestHost = new AtomicReference<>();

    public static void main(String[] args) throws Exception {
        File root=new File(args.length==0?".cache/jvm-check-state":args[0]).getCanonicalFile();
        if (root.exists()) throw new IOException("Use a fresh state directory: "+root);
        HttpServer server=HttpServer.create(new InetSocketAddress("127.0.0.1",0),0);
        origin="http://127.0.0.1:"+server.getAddress().getPort();
        server.createContext("/version",e->{
            requestHost.set(e.getRequestHeaders().getFirst("Host"));
            versionRequests.incrementAndGet();
            if (!e.getRequestURI().getQuery().contains("current_version=")) throw new IOException("Missing current_version");
            byte[] b=metadata.get().getBytes(StandardCharsets.UTF_8);
            e.getResponseHeaders().set("Content-Type","application/json"); e.sendResponseHeaders(200,b.length);
            try (OutputStream out=e.getResponseBody()) { out.write(b); }
        });
        server.createContext("/content.zip",e->{downloads.incrementAndGet();byte[] b=archive.get();e.sendResponseHeaders(200,b.length);try(OutputStream out=e.getResponseBody()){out.write(b);}});
        server.createContext("/unavailable",e->{e.sendResponseHeaders(503,-1);e.close();});
        server.createContext("/redirect",e->{e.getResponseHeaders().set("Location","http://localhost:"+server.getAddress().getPort()+"/content.zip");e.sendResponseHeaders(302,-1);e.close();});
        server.start();
        try {
            BundleUpdater.Options options=options();
            BundleUpdater updater=new BundleUpdater(root,"1.0.1",options);
            check(updater.current().id.equals("builtin"),"initial bundled fallback");
            byte[] good=zip("index.html","<html>v2</html>","assets/settings.json","{\"launchScene\":\"Game\"}","assets/sub/game.js","window.game=2");
            publish("1.0.2",good,hash(good));
            check(updater.checkAndUpdate(Arrays.asList(origin+"/unavailable",origin+"/version"),BundleUpdaterCheck::parse),"HTTP version failover + real ZIP upgrade");
            String v2=updater.current().id;
            ContentSession playing = new ContentSession();
            playing.selectOnce(updater.current());
            check(playing.beginStartupCheck() && !playing.beginStartupCheck(), "one version check per game session");
            check(updater.current().version.equals("1.0.2") && read(updater.resource(v2,"assets/sub/game.js")).equals("window.game=2"),"nested static resources retained");
            int before=downloads.get();
            check(!updater.checkAndUpdate(Collections.singletonList(origin+"/version"),BundleUpdaterCheck::parse) && downloads.get()==before,"equal version skips ZIP download");
            check(versionRequests.get()==2,"startup performs an actual version HTTP request");
            publish("1.0.3",good,String.join("",Collections.nCopies(64,"0")));
            rejected(updater,"checksum mismatch retains v2");
            byte[] traversal=zip("index.html","x","../outside.txt","bad");
            publish("1.0.3",traversal,hash(traversal)); rejected(updater,"path traversal retains v2");
            byte[] noEntry=zip("assets/game.js","x");
            publish("1.0.3",noEntry,hash(noEntry)); rejected(updater,"missing index.html retains v2");
            byte[] collision=zip("index.html","x","ASSETS/a","1","assets/A","2");
            publish("1.0.3",collision,hash(collision)); rejected(updater,"case-colliding paths retain v2");
            byte[] symlink=good.clone();
            int central=index(symlink,new byte[]{0x50,0x4b,0x01,0x02});
            symlink[central+5]=3;
            int mode=0120777; symlink[central+40]=(byte)mode;symlink[central+41]=(byte)(mode>>>8);
            publish("1.0.3",symlink,hash(symlink)); rejected(updater,"ZIP symlink is rejected");
            byte[] crc=zip("index.html","<html>CRC_MARKER</html>");
            int marker=index(crc,"CRC_MARKER".getBytes(StandardCharsets.UTF_8)); crc[marker]='X';
            publish("1.0.3",crc,hash(crc)); rejected(updater,"ZIP CRC corruption retains v2");
            options.maxFileBytes=4;
            publish("1.0.3",good,hash(good)); rejected(updater,"expanded file limit retains v2"); options.maxFileBytes=64L<<20;
            metadata.set("{\"version\":\"1.0.3\",\"last_version_url\":\""+origin+"/redirect\",\"sha256\":\""+hash(good)+"\"}");
            rejected(updater,"redirect outside origin allowlist retains v2");
            BundleUpdater.Options blocked=options(); blocked.allowedPrivateCidrs.clear();
            BundleUpdater privateUpdater=new BundleUpdater(new File(root,"private-block-test"),"1.0.1",blocked);
            boolean denied=false;
            try { privateUpdater.checkAndUpdate(Collections.singletonList(origin+"/version"),BundleUpdaterCheck::parse); } catch(Exception expected){denied=true;}
            check(denied && privateUpdater.current().id.equals("builtin"),"private IP blocked without explicit CIDR");
            byte[] v3=zip("index.html","<html>v3</html>","assets/game.js","window.game=3");
            publish("1.0.3",v3,hash(v3));
            check(updater.checkAndUpdate(Collections.singletonList(origin+"/version"),BundleUpdaterCheck::parse),"second valid update commits");
            check(read(updater.resource(v2,"index.html")).contains("v2"),"previous page generation survives active update");
            String v3id=updater.current().id;
            check(playing.selectOnce(updater.current()).id.equals(v2) && playing.selected().version.equals("1.0.2"), "background upgrade cannot change playing session");
            ContentSession next = new ContentSession();
            check(next.selectOnce(updater.current()).id.equals(v3id), "next session selects newly verified release");
            Files.write(updater.resource(v3id,"index.html").toPath(),"corrupt".getBytes(StandardCharsets.UTF_8));
            BundleUpdater restored=new BundleUpdater(root,"1.0.1",options);
            check(restored.current().version.equals("1.0.2"),"restart detects corrupt newest release and restores v2");
            File stage=new File(root,".stage-crash");stage.mkdirs();
            Files.write(new File(stage,"incomplete").toPath(),new byte[]{1});
            Files.write(new File(root,"active-999.properties").toPath(),"id=broken\nversion=\\uZZZZ".getBytes(StandardCharsets.UTF_8));
            restored=new BundleUpdater(root,"1.0.1",options);
            check(restored.current().version.equals("1.0.2") && !stage.exists(),"partial pointer/staging crash preserves valid release");
            Files.write(restored.resource(restored.current().id,"index.html").toPath(),new byte[]{0});
            restored=new BundleUpdater(root,"1.0.1",options);
            check(restored.current().id.equals("builtin") && restored.current().version.equals("1.0.1"),"all external releases corrupt: bundled fallback");
            publish("1.0.1",good,hash(good));
            AtomicInteger lookups = new AtomicInteger();
            BundleUpdater.Options pinned = options();
            pinned.resolver = host -> Collections.singletonList(InetAddress.getByAddress(lookups.incrementAndGet()==1 ? new byte[]{127,0,0,1} : new byte[]{127,0,0,2}));
            BundleUpdater pinnedUpdater = new BundleUpdater(new File(root,"dns-pin"),"1.0.1",pinned);
            String pinnedOrigin="http://pinned.test:"+server.getAddress().getPort();
            check(!pinnedUpdater.checkAndUpdate(Collections.singletonList(pinnedOrigin+"/version"),BundleUpdaterCheck::parse) && lookups.get()==1, "connection uses the checked DNS result without a second lookup");
            check(requestHost.get().startsWith("pinned.test:"), "pinned IP connection preserves the original HTTP hostname");
            BundleUpdater.Options mixed = options();
            mixed.resolver = host -> Arrays.asList(InetAddress.getByAddress(new byte[]{127,0,0,1}), InetAddress.getByAddress(new byte[]{127,0,0,2}));
            BundleUpdater mixedUpdater = new BundleUpdater(new File(root,"dns-mixed"),"1.0.1",mixed);
            int requestsBefore=versionRequests.get(); boolean mixedRejected=false;
            try { mixedUpdater.checkAndUpdate(Collections.singletonList(pinnedOrigin+"/version"),BundleUpdaterCheck::parse); } catch(Exception expected) { mixedRejected=true; }
            check(mixedRejected && versionRequests.get()==requestsBefore, "mixed allowed/blocked DNS answer is rejected before connecting");
            System.out.println("PASS "+passed+" production-core checks (JVM + real loopback HTTP; Android device not exercised)");
        } finally { server.stop(0); }
    }
    private static BundleUpdater.Options options(){BundleUpdater.Options o=new BundleUpdater.Options();o.allowHttp=true;o.allowedOrigins.add(origin);o.allowedPrivateCidrs.add("127.0.0.1/32");return o;}
    private static void publish(String version,byte[] zip,String sha){archive.set(zip);metadata.set("{\"version\":\""+version+"\",\"last_version_url\":\""+origin+"/content.zip\",\"sha256\":\""+sha+"\"}");}
    private static BundleUpdater.VersionInfo parse(String json)throws Exception{return new BundleUpdater.VersionInfo(field(json,"version"),field(json,"last_version_url"),field(json,"sha256"));}
    private static String field(String json,String key)throws IOException{Matcher m=Pattern.compile("\\\""+key+"\\\"\\s*:\\s*\\\"([^\\\"]*)\\\"").matcher(json);if(!m.find())throw new IOException("Missing metadata field");return m.group(1);}
    private static void rejected(BundleUpdater updater,String name)throws Exception{boolean rejected=false;try{updater.checkAndUpdate(Collections.singletonList(origin+"/version"),BundleUpdaterCheck::parse);}catch(Exception expected){rejected=true;}check(rejected&&updater.current().version.equals("1.0.2"),name);}
    private static void check(boolean value,String name){if(!value)throw new AssertionError(name);passed++;System.out.println("PASS "+name);}
    private static byte[] zip(String... entries)throws Exception{ByteArrayOutputStream b=new ByteArrayOutputStream();try(ZipOutputStream z=new ZipOutputStream(b)){for(int i=0;i<entries.length;i+=2){byte[] data=entries[i+1].getBytes(StandardCharsets.UTF_8);CRC32 crc=new CRC32();crc.update(data);ZipEntry e=new ZipEntry(entries[i]);e.setMethod(ZipEntry.STORED);e.setSize(data.length);e.setCrc(crc.getValue());z.putNextEntry(e);z.write(data);z.closeEntry();}}return b.toByteArray();}
    private static String hash(byte[] b)throws Exception{StringBuilder s=new StringBuilder();for(byte v:MessageDigest.getInstance("SHA-256").digest(b))s.append(String.format("%02x",v&255));return s.toString();}
    private static String read(File f)throws IOException{return new String(Files.readAllBytes(f.toPath()),StandardCharsets.UTF_8);}
    private static int index(byte[] haystack,byte[] needle){outer:for(int i=0;i<=haystack.length-needle.length;i++){for(int j=0;j<needle.length;j++)if(haystack[i+j]!=needle[j])continue outer;return i;}throw new AssertionError("Marker not found");}
}
