# lddx
This is a dynamic dependency lister for OS X/macOS that can:
* List dependencies recursively
* Output in a format similar to ldd
* Output in JSON format

Similar to dylibbundler, it can optionally collect and fix all dependencies required for a given binary. However, unlike dylibbundler:

* It is highly configurable
    * The ignore paths can be altered (defaults to `/usr/lib` and `/System`
    * Framework dependencies can also be bundled
    * Specific files can be ignored
* It is an order of magnitude faster
    * Dependencies are computed in parallel, and dependencies for each unique library are only computed once
    * It performs 'smart' fixing - that is, `install_name_tool` is only called for the libraries that the fixed library depends on
* It fixes libraries using @loader_path instead of @executable_path for more consistent results
* It can fix multiple files at once, even if said files are in different folders (the relative path the fixed libraries is automatically calculated)
* If a dependent library already exists in the output folder, its collection can be skipped and binaries be fixed to point to it
* It can recursively scan a folder for files to process
* The dependency calculator is cross-platform
    * Instead of `otool`, lddx uses the built-in Fat/Mach-O parser that comes with Go. This means that technically, the parser can work on any platform that Go supports, including Linux and Windows. Its usefulness is limited by the fact that for recursive dependencies to be solved, it must either be on the same Mac file system, or all its dependencies must be relative.

## Getting lddx
If you have Go installed, just run

    go get github.com/jtanx/lddx
    
Otherwise, please wait a while, I'll release some binaries when I'm fairly happy with the implementation.

# Caveats
**Be aware**, lddx is in a *super alpha* state; that is, it's really new and may be prone to breaking. Some of the features haven't been checked/perfected yet, so there may be many unresolved issues. Use at your own risk!

* Will not follow symlinked files/folders when searching for files to process
* May not play nice if you already have fixed paths (e.g. @executable_path/@loader_path/@rpath listed as a dependency)
* Haven't really tested on universal binaries
