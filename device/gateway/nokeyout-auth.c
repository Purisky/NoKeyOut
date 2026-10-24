/*
 * nokeyout-auth —— root 所有的 PAM 认证 helper（panel 降权后专用）· C 版
 *
 * 背景：panel 以 nokeyout 非 root 用户运行；PAM 校验「非本人账号」时，
 * unix_chkpwd 只允许 root 或账号本人调用（setuid helper 的硬约束），
 * 所以非 root 进程无法直接验证管理员口令。本 helper 以 root 运行完成 PAM 校验。
 *
 *
 * 协议（stdin，两行）：  user\npassword\n
 * 退出码：0=通过  1=拒绝  2=用法/后端错误
 *
 * sudoers（/etc/sudoers.d/nokeyout-panel）：
 *   nokeyout ALL=(root) NOPASSWD: ..., /usr/local/bin/nokeyout-auth
 *
 * 构建：cc -O2 -o nokeyout-auth nokeyout-auth.c -lpam
 */
#include <security/pam_appl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *g_password;
static char g_user[256], g_pass[1024];

static int conv(int num_msg, const struct pam_message **msg,
                struct pam_response **resp, void *appdata_ptr) {
    (void)appdata_ptr;
    /* PAM 要求 conv 返回 malloc 出来的响应数组 */
    struct pam_response *r = calloc((size_t)num_msg, sizeof(struct pam_response));
    if (!r) return PAM_BUF_ERR;
    for (int i = 0; i < num_msg; i++) {
        switch (msg[i]->msg_style) {
        case PAM_PROMPT_ECHO_OFF:
        case PAM_PROMPT_ECHO_ON:
            r[i].resp = strdup(g_password ? g_password : "");
            if (!r[i].resp) { free(r); return PAM_BUF_ERR; }
            break;
        default: /* PAM_ERROR_MSG / PAM_TEXT_INFO：不回内容 */
            r[i].resp = NULL;
            break;
        }
    }
    *resp = r;
    return PAM_SUCCESS;
}

int main(void) {
    if (!fgets(g_user, sizeof g_user, stdin)) {
        fprintf(stderr, "usage: user\\npassword\\n on stdin\n");
        return 2;
    }
    /* 第二行缺失按空口令处理（与 Python 版 split(\n) 语义一致：要求两段、首段非空） */
    if (!fgets(g_pass, sizeof g_pass, stdin)) g_pass[0] = '\0';
    g_user[strcspn(g_user, "\n")] = '\0';
    g_pass[strcspn(g_pass, "\n")] = '\0';
    if (g_user[0] == '\0') {
        fprintf(stderr, "usage: user\\npassword\\n on stdin\n");
        return 2;
    }
    g_password = g_pass;

    /* 与 Python 版相同的服务优先级：login 先，common-auth 兜底 */
    static const char *services[] = {"login", "common-auth"};
    for (size_t s = 0; s < sizeof services / sizeof services[0]; s++) {
        struct pam_conv pconv = {conv, NULL};
        pam_handle_t *pamh = NULL;
        int rc = pam_start(services[s], g_user, &pconv, &pamh);
        if (rc != PAM_SUCCESS) continue;
        rc = pam_authenticate(pamh, 0);
        int rc2 = pam_acct_mgmt(pamh, 0);
        pam_end(pamh, rc);
        if (rc == PAM_SUCCESS && rc2 == PAM_SUCCESS) return 0;
        /* 认证失败（拒凭证）不换服务；换服务只覆盖「服务不可用」类错误 */
        if (rc == PAM_AUTH_ERR || rc == PAM_USER_UNKNOWN) return 1;
    }
    return 1;
}
