#import <Cocoa/Cocoa.h>

extern void goWakeCallback(void);

@interface WakeObserver : NSObject
@end

@implementation WakeObserver

- (void)handleWake:(NSNotification *)notification {
    goWakeCallback();
}

@end

void RegisterWakeNotification(void) {
    static WakeObserver *observer = nil;
    if (observer == nil) {
        observer = [[WakeObserver alloc] init];
        [[[NSWorkspace sharedWorkspace] notificationCenter]
            addObserver:observer
               selector:@selector(handleWake:)
                   name:NSWorkspaceDidWakeNotification
                 object:nil];
    }
}
