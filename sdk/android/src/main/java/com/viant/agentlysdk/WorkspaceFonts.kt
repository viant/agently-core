package com.viant.agentlysdk

import kotlinx.serialization.Serializable

@Serializable
data class WorkspaceFontAsset(val href: String, val format: String, val sha256: String, val sizeBytes: Int) {
    val isNative: Boolean get() = format in setOf("ttf", "otf") && sha256.matches(Regex("[a-f0-9]{64}")) &&
        href == "/v1/workspace/ui/fonts/$sha256.$format" && sizeBytes in 1..1048576
}
@Serializable
data class WorkspaceFontFace(val style: String, val weight: String, val unicodeRange: String? = null,
    val web: WorkspaceFontAsset? = null, val native: WorkspaceFontAsset? = null)
@Serializable
data class WorkspaceFontFamily(val role: String, val name: String, val fallback: String = "system", val faces: List<WorkspaceFontFace> = emptyList())
