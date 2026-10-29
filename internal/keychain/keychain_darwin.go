//go:build darwin

package keychain

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
static CFMutableDictionaryRef secretQuery() {
 CFMutableDictionaryRef q=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFDictionarySetValue(q,kSecClass,kSecClassGenericPassword);
 CFDictionarySetValue(q,kSecAttrService,CFSTR("dev.cham.midrive"));
 CFDictionarySetValue(q,kSecAttrAccount,CFSTR("xiaomi-session"));
 return q;
}
static OSStatus saveSecret(const void* value, int size) {
 CFMutableDictionaryRef q=secretQuery();
 CFDataRef data=CFDataCreate(NULL,(const UInt8*)value,size);
 CFMutableDictionaryRef attrs=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFDictionarySetValue(attrs,kSecValueData,data);
 OSStatus s=SecItemUpdate(q,attrs);
 if(s==errSecItemNotFound){CFDictionarySetValue(q,kSecValueData,data);CFDictionarySetValue(q,kSecAttrAccessible,kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly);s=SecItemAdd(q,NULL);}
 CFRelease(attrs);CFRelease(data);CFRelease(q);return s;
}
static OSStatus loadSecret(void **data, int *size) {
 CFMutableDictionaryRef q=secretQuery();CFDictionarySetValue(q,kSecReturnData,kCFBooleanTrue);CFDictionarySetValue(q,kSecMatchLimit,kSecMatchLimitOne);
 CFTypeRef result=NULL;OSStatus s=SecItemCopyMatching(q,&result);CFRelease(q);
 if(s==0&&result){*size=(int)CFDataGetLength((CFDataRef)result);*data=malloc(*size);memcpy(*data,CFDataGetBytePtr((CFDataRef)result),*size);CFRelease(result);}
 return s;
}
static OSStatus deleteSecret(){CFMutableDictionaryRef q=secretQuery();OSStatus s=SecItemDelete(q);CFRelease(q);return s==errSecItemNotFound?0:s;}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

func Save(b []byte) error {
	p := C.CBytes(b)
	defer C.free(p)
	if s := C.saveSecret(p, C.int(len(b))); s != 0 {
		return fmt.Errorf("无法保存到 macOS 钥匙串（%d）", int(s))
	}
	return nil
}
func Load() ([]byte, error) {
	var p unsafe.Pointer
	var n C.int
	s := C.loadSecret(&p, &n)
	if s == C.errSecItemNotFound {
		return nil, nil
	}
	if s != 0 {
		return nil, fmt.Errorf("无法读取 macOS 钥匙串（%d）", int(s))
	}
	defer C.free(p)
	return C.GoBytes(p, n), nil
}
func Delete() error {
	if s := C.deleteSecret(); s != 0 {
		return fmt.Errorf("无法移除登录会话（%d）", int(s))
	}
	return nil
}
