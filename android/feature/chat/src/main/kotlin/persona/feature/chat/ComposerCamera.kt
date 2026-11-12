package persona.feature.chat

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.ContextCompat

/*
 * "Take photo" (iOS CameraPicker): the system camera writes into cache/chat-captures through the chat FileProvider, the photo
 * then goes in like a picked one. Those originals belong to one draft and are deleted when they leave it.
 */

/** A photo this app took for a draft (cache/chat-captures behind the chat FileProvider), not a picked one. */
internal fun isCaptureUri(uri: String, packageName: String): Boolean = uri.startsWith("content://$packageName.chat.files/chat-captures/")

/**
 * The captures that were in [before] and are no longer in [now] (removed, or sent and cleared), by attachment id. An
 * edited photo's source ([keptBefore] / [keptNow], #4992) stays while the photo it was edited into is in the draft.
 */
internal fun leftCaptures(before: List<ComposerAttachment>, now: List<ComposerAttachment>, packageName: String,
                          keptBefore: Collection<String> = emptyList(), keptNow: Collection<String> = emptyList()): List<String> =
    (before.filter { old -> now.none { it.id == old.id } }.map { it.uri } + keptBefore)
        .filter { uri -> isCaptureUri(uri, packageName) && uri !in keptNow && now.none { it.uri == uri } }.distinct()

/** Deletes a capture's file through the provider; anything that is not one of ours is left alone. Call off the main thread. */
internal fun deleteCapture(context: Context, uri: String) {
    if (!isCaptureUri(uri, context.packageName)) return
    runCatching { context.contentResolver.delete(Uri.parse(uri), null, null) }
}

/**
 * Whether the system camera may be started now. The app declares CAMERA (pairing scans codes), and then Android
 * refuses ACTION_IMAGE_CAPTURE until it is granted; a build that does not declare it needs nothing.
 */
internal fun cameraAllowed(context: Context): Boolean =
    ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED || !declaresCamera(context)

private fun declaresCamera(context: Context): Boolean = runCatching {
    val manager = context.packageManager
    val info = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU)
        manager.getPackageInfo(context.packageName, PackageManager.PackageInfoFlags.of(PackageManager.GET_PERMISSIONS.toLong()))
    else @Suppress("DEPRECATION") manager.getPackageInfo(context.packageName, PackageManager.GET_PERMISSIONS)
    info.requestedPermissions.orEmpty().contains(Manifest.permission.CAMERA)
}.getOrDefault(true)

/** The app's name as the system settings list it (the label is configurable per build). */
internal fun appLabel(context: Context): String =
    runCatching { context.applicationInfo.loadLabel(context.packageManager).toString() }.getOrNull()?.takeIf(String::isNotBlank) ?: "Persona"

/** This app's page in the system settings, where its permissions are. */
internal fun openAppSettings(context: Context) {
    val intent = Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null))
    runCatching { context.startActivity(intent) }
        .recoverCatching { context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
}
