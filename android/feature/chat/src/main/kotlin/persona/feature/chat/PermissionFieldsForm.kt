package persona.feature.chat

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import persona.core.contracts.InteractionField
import persona.core.designsystem.DecisionButtons
import persona.core.designsystem.JAction
import persona.core.designsystem.JSecretField
import persona.core.designsystem.JTextField
import persona.core.designsystem.Tokens

/**
 * A permission card's fields (#4641, iOS `PermissionInputForm`): one field per declared field, a secret one masked, then
 * Deny and Fill In & Allow (no Always); Allow stays off until every required field is filled ([canAllow]). The caller
 * holds [values] ([rememberTransient]) and empties them when it sends either answer; they are emptied too when the form
 * leaves the screen while the card stays. Control characters never get into a secret field (one pasted line break would
 * hide behind the mask).
 */
@Composable
internal fun PermissionFieldsActions(
    fields: List<InteractionField>, values: Map<String, String>, onValuesChange: (Map<String, String>) -> Unit, editable: Boolean,
    canDeny: Boolean, canAllow: Boolean, onDeny: () -> Unit, onAllow: () -> Unit, modifier: Modifier = Modifier,
) {
    val clear by rememberUpdatedState(onValuesChange)
    DisposableEffect(Unit) { onDispose { clear(emptyMap()) } }
    Column(modifier, verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        fields.forEach { field ->
            val value = values[field.key].orEmpty()
            val tagged = Modifier.fillMaxWidth().testTag("interaction.field.${field.key}")
            if (field.secret) JSecretField(value, { typed -> onValuesChange(values + (field.key to typed.filterNot(Char::isISOControl))) },
                label = field.label, modifier = tagged, enabled = editable)
            else JTextField(value, { typed -> onValuesChange(values + (field.key to typed)) }, tagged,
                placeholder = field.placeholder ?: field.label, label = field.label, singleLine = true, enabled = editable)
        }
        DecisionButtons(
            primary = JAction(stringResource(R.string.chat_request_fill_in_allow), enabled = canAllow, onClick = onAllow),
            secondary = JAction(stringResource(R.string.chat_request_deny), enabled = canDeny, onClick = onDeny),
        )
    }
}
