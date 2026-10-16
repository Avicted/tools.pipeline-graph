import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.*
import jetbrains.buildServer.configs.kotlin.triggers.*

version = "2024.03"

val imageName = "registry.acme.io/service-a"

project {
    buildType(Build)
    buildType(Package)
    subProject(Deploy)
}

object Build : BuildType({
    name = "Build & Test"
    artifactRules = """
        +:dist/service-a.tar.gz
        +:reports/** => reports.zip
    """.trimIndent()
    steps {
        script {
            name = "build"
            scriptContent = "make build test"
        }
        exec {
            name = "web"
            path = "make"
            arguments = "-C web bundle"
        }
    }
    dependencies {
        snapshot(AbsoluteId("PlatformBase_BuildBaseImage")) {
            onDependencyFailure = FailureAction.FAIL_TO_START
        }
    }
    triggers {
        vcs { }
    }
})

object Package : BuildType({
    name = "Package image"
    steps {
        script {
            scriptContent = """
                docker build -t $imageName:%build.number% --build-arg BASE=registry.acme.io/base:latest .
                make publish IMAGE=$imageName
            """.trimIndent()
        }
    }
    dependencies {
        dependency(Build) {
            snapshot { }
            artifacts {
                artifactRules = "service-a.tar.gz => dist/"
            }
        }
    }
})
