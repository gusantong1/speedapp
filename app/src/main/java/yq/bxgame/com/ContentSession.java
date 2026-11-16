package yq.bxgame.com;

import java.util.UUID;

/** 一次游戏会话固定一份完整资源；准备好的更新不改变正在运行的会话。 */
public final class ContentSession {
    public final String id = UUID.randomUUID().toString();
    private volatile BundleUpdater.State selected;
    private boolean checkingStarted;

    public synchronized BundleUpdater.State selectOnce(BundleUpdater.State available) {
        if (selected == null) selected = available;
        return selected;
    }
    public BundleUpdater.State selected() { return selected; }
    public synchronized boolean beginStartupCheck() {
        if (checkingStarted) return false;
        checkingStarted = true;
        return true;
    }
}
