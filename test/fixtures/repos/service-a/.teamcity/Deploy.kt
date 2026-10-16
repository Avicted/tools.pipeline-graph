package _Self

import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.script
import jetbrains.buildServer.configs.kotlin.triggers.finishBuildTrigger

fun deploy(env: String, auto: Boolean) = BuildType {
    id("Deploy_${env}")
    name = "Deploy to $env"
    steps {
        script {
            scriptContent = "make deploy ENV=$env"
        }
    }
    dependencies {
        snapshot(Package) {}
    }
    if (auto) {
        triggers {
            finishBuildTrigger {
                buildType = "${Package.id}"
                successfulOnly = true
            }
        }
    }
}

object Deploy : Project({
    name = "Deploy"
    sequential {
        buildType(deploy("staging", true))
        buildType(deploy("prod", false))
    }
})
