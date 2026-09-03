# Add project specific ProGuard rules here.
# Keep kotlinx.serialization @Serializable classes and their companion objects.
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.**

-keepclassmembers class kotlinx.serialization.json.** {
    *** Companion;
}
-keepclasseswithmembers class kotlinx.serialization.json.** {
    kotlinx.serialization.KSerializer serializer(...);
}

# Keep data models (they are reflectively accessed by serialization).
-keep,includedescriptorclasses class io.github.pelico.ddnas.data.model.** { *; }

# OkHttp / Retrofit platform rules (auto-applied via consumer rules, kept for safety).
-dontwarn okhttp3.internal.platform.**
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**
-dontwarn org.openjsse.**
