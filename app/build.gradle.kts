import com.android.build.gradle.ProguardFiles.getDefaultProguardFile

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "yq.bxgame.com"
    compileSdk = 33

    defaultConfig {
        applicationId = "yq.bxgame.com"
        minSdk = 24
        targetSdk = 33
        versionCode = 1
        versionName = "1.0"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    signingConfigs {
        create("release") {
            // 签名文件路径
            storeFile = file("henry20230831114241-keystore.jks")
            // 签名密码
            storePassword = "123456"
            // 别名
            keyAlias = "henry20230831114241"
            // 别名密码
            keyPassword = "123456"

            enableV1Signing = true
            enableV2Signing = true
            enableV3Signing = false
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
            signingConfig = signingConfigs.getByName("release")
        }

        debug {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_1_8
        targetCompatibility = JavaVersion.VERSION_1_8
    }
    kotlinOptions {
        jvmTarget = "1.8"
    }
    buildFeatures {
        viewBinding = true
    }
}

dependencies {

    implementation("androidx.core:core-ktx:1.9.0")
    implementation("androidx.appcompat:appcompat:1.6.1")
    implementation("com.google.android.material:material:1.8.0")
    implementation("androidx.constraintlayout:constraintlayout:2.1.4")
    implementation("androidx.navigation:navigation-fragment-ktx:2.5.3")
    implementation("androidx.navigation:navigation-ui-ktx:2.5.3")
    implementation("com.tencent.tbs:tbssdk:44286")
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test.ext:junit:1.1.5")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.5.1")
}
//plugins {
//    id 'com.android.application'
//    id 'kotlin-android'
//}
//
//android {
//    compileSdkVersion 31
//    buildToolsVersion "30.0.3"
//
//    defaultConfig {
//        applicationId "com.example.x5webviewdemo"
//        minSdkVersion 21
//        targetSdkVersion 31
//        versionCode 1
//        versionName "1.0"
//    }
//
//    buildTypes {
//        release {
//            minifyEnabled false
//            proguardFiles getDefaultProguardFile('proguard-android-optimize.txt'), 'proguard-rules.pro'
//        }
//    }
//
//    compileOptions {
//        sourceCompatibility JavaVersion.VERSION_1_8
//                targetCompatibility JavaVersion.VERSION_1_8
//    }
//
//    kotlinOptions {
//        jvmTarget = '1.8'
//    }
//}
//
//dependencies {
//    implementation fileTree(org.gradle.internal.impldep.bsh.commands.dir: 'libs', include: ['*.jar'])
//    implementation 'com.tencent.tbs:tbssdk:44286' // X5 内核
//
//    implementation "org.jetbrains.kotlin:kotlin-stdlib:$kotlin_version"
//    implementation 'androidx.core:core-ktx:1.7.0'
//    implementation 'androidx.appcompat:appcompat:1.4.0'
//    implementation 'com.google.android.material:material:1.4.0'
//    implementation 'androidx.constraintlayout:constraintlayout:2.1.2'
//}
//plugins {
//    id("com.android.application")
//    id("org.jetbrains.kotlin.android")
//}
//
//android {
//    namespace = "com.example.webview_app"
//    compileSdk = 33
//
//    defaultConfig {
//        applicationId = "com.example.webview_app"
//        minSdk = 24
//        targetSdk = 33
//        versionCode = 1
//        versionName = "1.0"
//
//        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
//    }
//
//    signingConfigs {
//        create("release") {
//            // 签名文件路径
//            storeFile = file("henry20230831114241-keystore.jks")
//            // 签名密码
//            storePassword = "123456"
//            // 别名
//            keyAlias = "henry20230831114241"
//            // 别名密码
//            keyPassword = "123456"
//
//            enableV1Signing = true
//            enableV2Signing = true
//            enableV3Signing = false
//        }
//    }
//
//    buildTypes {
//        release {
//            isMinifyEnabled = true
//            proguardFiles(
//                getDefaultProguardFile("proguard-android-optimize.txt"),
//                "proguard-rules.pro"
//            )
//            signingConfig = signingConfigs.getByName("release")
//        }
//
//        debug {
//            isMinifyEnabled = false
//            proguardFiles(
//                getDefaultProguardFile("proguard-android-optimize.txt"),
//                "proguard-rules.pro"
//            )
//        }
//    }
//    compileOptions {
//        sourceCompatibility = JavaVersion.VERSION_1_8
//        targetCompatibility = JavaVersion.VERSION_1_8
//    }
//    kotlinOptions {
//        jvmTarget = "1.8"
//    }
//    buildFeatures {
//        viewBinding = true
//    }
//}
//
//dependencies {
//
//    implementation("androidx.core:core-ktx:1.9.0")
//    implementation("androidx.appcompat:appcompat:1.6.1")
//    implementation("com.google.android.material:material:1.8.0")
//    implementation("androidx.constraintlayout:constraintlayout:2.1.4")
//    implementation("androidx.navigation:navigation-fragment-ktx:2.5.3")
//    implementation("androidx.navigation:navigation-ui-ktx:2.5.3")
//    implementation("com.tencent.tbs:tbssdk:44286")
//    testImplementation("junit:junit:4.13.2")
//    androidTestImplementation("androidx.test.ext:junit:1.1.5")
//    androidTestImplementation("androidx.test.espresso:espresso-core:3.5.1")
//}