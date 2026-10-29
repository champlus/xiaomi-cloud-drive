#include <Security/Security.h>
#include <CommonCrypto/CommonDigest.h>
#include <CoreFoundation/CoreFoundation.h>
#include <unistd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifndef APP_IDENTIFIER
#define APP_IDENTIFIER "dev.cham.midrive"
#endif
#ifndef VAULT_SERVICE
#define VAULT_SERVICE "dev.cham.midrive.credentials"
#endif
#define VAULT_ACCOUNT "web-session-v3"
#define MAX_ARCHIVE (1024*1024)

// This small broker has an identity independent of the UI's version and code.
// Only its actual, live parent signed with our certificate and app ID may call it.
static int authorized_parent(void) {
 SecCodeRef self=NULL,parent=NULL;CFDictionaryRef info=NULL;SecRequirementRef requirement=NULL;
 CFNumberRef number=NULL;CFDictionaryRef attributes=NULL;CFStringRef expression=NULL;
 int allowed=0;pid_t pid=getppid();if(pid<=1)return 0;
 if(SecCodeCopySelf(kSecCSDefaultFlags,&self)!=errSecSuccess)goto done;
 if(SecCodeCopySigningInformation(self,kSecCSSigningInformation,&info)!=errSecSuccess)goto done;
 CFArrayRef certificates=CFDictionaryGetValue(info,kSecCodeInfoCertificates);
 if(!certificates || CFArrayGetCount(certificates)==0)goto done;
 CFDataRef der=SecCertificateCopyData((SecCertificateRef)CFArrayGetValueAtIndex(certificates,0));
 if(!der)goto done;
 unsigned char digest[CC_SHA1_DIGEST_LENGTH];CC_SHA1(CFDataGetBytePtr(der),(CC_LONG)CFDataGetLength(der),digest);CFRelease(der);
 char fingerprint[CC_SHA1_DIGEST_LENGTH*2+1];for(int i=0;i<CC_SHA1_DIGEST_LENGTH;i++)sprintf(fingerprint+i*2,"%02x",digest[i]);
 expression=CFStringCreateWithFormat(NULL,NULL,CFSTR("identifier \"%s\" and certificate leaf = H\"%s\""),APP_IDENTIFIER,fingerprint);
 if(SecRequirementCreateWithString(expression,kSecCSDefaultFlags,&requirement)!=errSecSuccess)goto done;
 number=CFNumberCreate(NULL,kCFNumberIntType,&pid);
 const void *keys[]={kSecGuestAttributePid};const void *values[]={number};
 attributes=CFDictionaryCreate(NULL,keys,values,1,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 if(SecCodeCopyGuestWithAttributes(NULL,attributes,kSecCSDefaultFlags,&parent)!=errSecSuccess)goto done;
 allowed=SecCodeCheckValidity(parent,kSecCSStrictValidate,requirement)==errSecSuccess;
 done:
 if(expression)CFRelease(expression);if(attributes)CFRelease(attributes);if(number)CFRelease(number);
 if(requirement)CFRelease(requirement);if(info)CFRelease(info);if(parent)CFRelease(parent);if(self)CFRelease(self);
 return allowed;
}
int main(int argc,char **argv) {
 if(argc!=2 || (strcmp(argv[1],"read") && strcmp(argv[1],"write") && strcmp(argv[1],"delete-test")))return 64;
 if(!authorized_parent())return 77;
 fputs("MPVAULT1\n",stdout);fflush(stdout);
 char acknowledgement[3];if(fread(acknowledgement,1,3,stdin)!=3 || memcmp(acknowledgement,"GO\n",3))return 77;
 SecKeychainSetUserInteractionAllowed(false);
 const void *keys[]={kSecClass,kSecAttrService,kSecAttrAccount};
 const void *values[]={kSecClassGenericPassword,CFSTR(VAULT_SERVICE),CFSTR(VAULT_ACCOUNT)};
 CFMutableDictionaryRef query=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 for(int i=0;i<3;i++)CFDictionarySetValue(query,keys[i],values[i]);
 OSStatus status=errSecParam;
 if(!strcmp(argv[1],"read")) {
  CFDictionarySetValue(query,kSecReturnData,kCFBooleanTrue);CFTypeRef value=NULL;
  status=SecItemCopyMatching(query,&value);
  if(status==errSecSuccess && value && CFGetTypeID(value)==CFDataGetTypeID()) {
   CFIndex size=CFDataGetLength(value);
   if(size>MAX_ARCHIVE)status=errSecDataTooLarge;
   else if(fwrite(CFDataGetBytePtr(value),1,(size_t)size,stdout)!=(size_t)size)status=errSecIO;
  }
  if(value)CFRelease(value);
 }else if(!strcmp(argv[1],"write")) {
  unsigned char *buffer=malloc(MAX_ARCHIVE+1);if(!buffer){CFRelease(query);return 71;}
  size_t size=fread(buffer,1,MAX_ARCHIVE+1,stdin);
  if(ferror(stdin)||size>MAX_ARCHIVE){free(buffer);CFRelease(query);return 65;}
  CFDataRef data=CFDataCreate(NULL,buffer,(CFIndex)size);memset(buffer,0,size);free(buffer);
  const void *updateKeys[]={kSecValueData};const void *updateValues[]={data};
  CFDictionaryRef update=CFDictionaryCreate(NULL,updateKeys,updateValues,1,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
  status=SecItemUpdate(query,update);
  if(status==errSecItemNotFound){CFDictionarySetValue(query,kSecValueData,data);CFDictionarySetValue(query,kSecAttrAccessible,kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly);status=SecItemAdd(query,NULL);}
  CFRelease(update);CFRelease(data);
 }else {
#ifdef TEST_VAULT
  status=SecItemDelete(query);
#endif
 }
 CFRelease(query);
 if(status==errSecSuccess)return 0;
 if(status==errSecItemNotFound)return 44;
 if(status==errSecInteractionNotAllowed || status==errSecAuthFailed)return 70;
 // Do not print data, cookie values or raw request payloads on any error.
 return 74;
}
