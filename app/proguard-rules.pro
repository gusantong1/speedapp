# 保留 X5 内核需要的类
-keep class dalvik.system.VMStack { *; }
-keep class com.tencent.smtt.** { *; }
-keep class com.tencent.tbs.** { *; }

# 保留所有 native 方法
-keepclasseswithmembernames class * {
    native <methods>;
}

# 保留注解
-keepattributes *Annotation*
# Please add these rules to your existing keep rules in order to suppress warnings.
# This is generated automatically by the Android Gradle plugin.
-dontwarn dalvik.system.VMStack
# X5 内核保留规则
-keep class com.tencent.smtt.** { *; }
-keep class com.tencent.tbs.** { *; }
-keep class dalvik.system.** { *; }
-keep class org.chromium.** { *; }

# 保留 JNI 相关
-keepclasseswithmembernames class * {
    native <methods>;
}

# 保留 WebView 相关
-keep public class * extends android.webkit.WebViewClient
-keep public class * extends android.webkit.WebChromeClient

# 保留 JavaScript 接口
-keepclassmembers class * {
    @android.webkit.JavascriptInterface <methods>;
}