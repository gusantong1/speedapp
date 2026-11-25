package yq.bxgame.com;

import android.app.Application;
import android.os.Bundle;
import android.webkit.WebView;
import org.junit.*;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.shadows.ShadowLooper;
import org.robolectric.shadows.ShadowWebView;
import org.json.JSONArray;
import org.json.JSONObject;
import okhttp3.mockwebserver.*;
import okio.Buffer;
import java.io.ByteArrayOutputStream;
import java.lang.reflect.Field;
import java.net.InetAddress;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.UUID;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.zip.*;
import static org.junit.Assert.*;

/** Production Activity + real loopback update; WebView is shadowed, not a device/rendering test. */
@RunWith(RobolectricTestRunner.class)
@Config(sdk=33, application=Application.class, shadows=StartupLifecycleTest.CountingWebView.class)
public class StartupLifecycleTest {
    private MockWebServer server;
    private final CountDownLatch releaseVersion=new CountDownLatch(1);
    private final AtomicInteger metadataRequests=new AtomicInteger();
    private ContentManager manager;
    @Implements(WebView.class)
    public static class CountingWebView extends ShadowWebView {
        static final AtomicInteger loads=new AtomicInteger();
        @Implementation protected void loadUrl(String url) { loads.incrementAndGet(); super.loadUrl(url); }
    }
    @Before public void reset() throws Exception {
        CountingWebView.loads.set(0);
        Field instance=ContentManager.class.getDeclaredField("instance");instance.setAccessible(true);instance.set(null,null);
        manager=ContentManager.Companion.get(RuntimeEnvironment.getApplication());
        server=new MockWebServer();server.start(InetAddress.getByName("127.0.0.1"),0);
        String origin=server.url("/").newBuilder().host("127.0.0.1").build().toString();origin=origin.substring(0,origin.length()-1);
        ByteArrayOutputStream bytes=new ByteArrayOutputStream();
        try(ZipOutputStream zip=new ZipOutputStream(bytes)) {zip.putNextEntry(new ZipEntry("index.html"));zip.write("<html>verified version 2</html>".getBytes(StandardCharsets.UTF_8));zip.closeEntry();}
        byte[] content=bytes.toByteArray();StringBuilder sha=new StringBuilder();for(byte b:MessageDigest.getInstance("SHA-256").digest(content))sha.append(String.format("%02x",b&255));
        String version=new JSONObject().put("version","1.0.2").put("last_version_url",origin+"/content.zip").put("sha256",sha.toString()).toString();
        server.setDispatcher(new Dispatcher(){@Override public MockResponse dispatch(RecordedRequest request)throws InterruptedException{
            if(request.getPath().startsWith("/version")){metadataRequests.incrementAndGet();releaseVersion.await(15,TimeUnit.SECONDS);return new MockResponse().setBody(version);}
            if(request.getPath().equals("/content.zip"))return new MockResponse().setBody(new Buffer().write(content));
            return new MockResponse().setResponseCode(404);
        }});
        JSONObject config=(JSONObject)field(manager,"config");config.put("buildId","lifecycle-"+UUID.randomUUID());config.put("versionEndpoints",new JSONArray().put(origin+"/version"));
        BundleUpdater.Options options=(BundleUpdater.Options)field(manager,"options");options.allowHttp=true;options.timeoutSec=15;options.allowedOrigins.add(origin);options.allowedPrivateCidrs.add("127.0.0.1/32");
    }
    @After public void stop()throws Exception{releaseVersion.countDown();server.shutdown();}

    @Test public void startupDomainIsExposedBeforeTheBundledPageRuns() throws Exception {
        JSONObject config = (JSONObject) field(manager, "config");
        config.put("getdomin", "dapi.chongyang.bid");
        config.put("dominList", new JSONArray().put("https://dapi.chongyang.bid").put("https://backup.example.com"));
        X5WebActivity.WebAppInterface bridge = new X5WebActivity.WebAppInterface(
            RuntimeEnvironment.getApplication(), manager, manager.openSession(null)
        );
        assertEquals("dapi.chongyang.bid", bridge.getdomin());
        String baseUrl = "https://dapi.chongyang.bid/api/game/v1";
        assertEquals(baseUrl, bridge.getGameApiBaseUrl());
        assertEquals(baseUrl, new JSONObject(bridge.getPackConfig()).getString("GAME_API_BASE_URL"));

        ContentManager.Resource page = manager.intercept(
            ContentManager.ORIGIN + "/html/" + BundleUpdater.BUILTIN + "/index.html"
        );
        assertNotNull(page);
        assertEquals(200, page.getStatus());
        try (java.io.InputStream input = page.getStream()) {
            String html = new String(input.readAllBytes(), StandardCharsets.UTF_8);
            assertTrue(html.startsWith("<!doctype html>"));
            int initializer = html.indexOf("window.getdomin=window.Android.getdomin();");
            assertTrue(initializer > 0 && initializer < html.indexOf("<body>"));
            int apiInitializer = html.indexOf("window.GAME_API_BASE_URL=window.Android.getGameApiBaseUrl();");
            assertTrue(apiInitializer > initializer && apiInitializer < html.indexOf("<body>"));
        }
    }

    @Test public void foregroundAndLateRealUpdateCannotReloadAnActiveLocalGame() throws Exception {
        ActivityController<X5WebActivity> c=Robolectric.buildActivity(X5WebActivity.class).create().start().resume().visible();
        try {
            awaitLoads(1);awaitMetadata(1);
            ContentSession session=session(c.get());assertEquals("1.0.1",session.selected().version);
            c.pause().stop().start().resume().visible();
            releaseVersion.countDown();awaitPreparedVersion();drain();
            assertSame(session,session(c.get()));
            assertEquals("late downloaded update must not reload the playing page",1,CountingWebView.loads.get());
            assertEquals("1.0.1",new JSONObject(manager.publicConfig(session)).getString("version"));
            assertEquals("foreground return must not start another version check",1,metadataRequests.get());
        } finally {c.pause().stop().destroy();}
    }
    @Test public void recreationPinsOldVersionAndNewLaunchSelectsPreparedVersion() throws Exception {
        ActivityController<X5WebActivity> first=Robolectric.buildActivity(X5WebActivity.class).create().start().resume().visible();
        awaitLoads(1);awaitMetadata(1);ContentSession original=session(first.get());
        releaseVersion.countDown();awaitPreparedVersion();
        Bundle saved=new Bundle();first.saveInstanceState(saved).pause().stop().destroy();
        ActivityController<X5WebActivity> restored=Robolectric.buildActivity(X5WebActivity.class).create(saved).start().restoreInstanceState(saved).resume().visible();
        try {
            awaitLoads(2);assertSame(original,session(restored.get()));assertEquals("1.0.1",session(restored.get()).selected().version);assertEquals(1,metadataRequests.get());
            ActivityController<X5WebActivity> fresh=Robolectric.buildActivity(X5WebActivity.class).create().start().resume().visible();
            try {awaitLoads(3);awaitMetadata(2);assertNotSame(original,session(fresh.get()));assertEquals("1.0.2",session(fresh.get()).selected().version);}
            finally {fresh.pause().stop().destroy();}
        } finally {restored.pause().stop().destroy();}
    }
    private void awaitPreparedVersion() throws Exception {waitFor(()->{BundleUpdater u=(BundleUpdater)field(manager,"updater");return u!=null&&u.current().version.equals("1.0.2");});}
    private void awaitMetadata(int expected) throws Exception {waitFor(()->metadataRequests.get()>=expected);assertEquals(expected,metadataRequests.get());}
    private static Object field(Object object,String name)throws Exception{Field f=object.getClass().getDeclaredField(name);f.setAccessible(true);return f.get(object);}
    private static ContentSession session(X5WebActivity activity)throws Exception{return(ContentSession)field(activity,"contentSession");}
    private static void awaitLoads(int expected)throws Exception{waitFor(()->CountingWebView.loads.get()>=expected);assertEquals(expected,CountingWebView.loads.get());drain();}
    private interface Condition{boolean get()throws Exception;}
    private static void waitFor(Condition condition)throws Exception{long end=System.nanoTime()+10000000000L;while(!condition.get()&&System.nanoTime()<end){ShadowLooper.runUiThreadTasksIncludingDelayedTasks();Thread.sleep(10);}assertTrue("lifecycle/update condition timed out",condition.get());}
    private static void drain()throws Exception{for(int i=0;i<10;i++){ShadowLooper.runUiThreadTasksIncludingDelayedTasks();Thread.sleep(10);}}
}
